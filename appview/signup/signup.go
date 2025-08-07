package signup

import (
	"bufio"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/posthog/posthog-go"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/dns"
	"tangled.sh/tangled.sh/core/appview/email"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/state/userutil"
	"tangled.sh/tangled.sh/core/appview/xrpcclient"
	"tangled.sh/tangled.sh/core/idresolver"
)

type Signup struct {
	config              *config.Config
	db                  *db.DB
	cf                  *dns.Cloudflare
	posthog             posthog.Client
	xrpc                *xrpcclient.Client
	idResolver          *idresolver.Resolver
	pages               *pages.Pages
	l                   *slog.Logger
	disallowedNicknames map[string]bool
}

func New(cfg *config.Config, database *db.DB, pc posthog.Client, idResolver *idresolver.Resolver, pages *pages.Pages, l *slog.Logger) *Signup {
	var cf *dns.Cloudflare
	if cfg.Cloudflare.ApiToken != "" && cfg.Cloudflare.ZoneId != "" {
		var err error
		cf, err = dns.NewCloudflare(cfg)
		if err != nil {
			l.Warn("failed to create cloudflare client, signup will be disabled", "error", err)
		}
	}

	disallowedNicknames := loadDisallowedNicknames(cfg.Core.DisallowedNicknamesFile, l)

	return &Signup{
		config:              cfg,
		db:                  database,
		posthog:             pc,
		idResolver:          idResolver,
		cf:                  cf,
		pages:               pages,
		l:                   l,
		disallowedNicknames: disallowedNicknames,
	}
}

func loadDisallowedNicknames(filepath string, logger *slog.Logger) map[string]bool {
	disallowed := make(map[string]bool)

	if filepath == "" {
		logger.Debug("no disallowed nicknames file configured")
		return disallowed
	}

	file, err := os.Open(filepath)
	if err != nil {
		logger.Warn("failed to open disallowed nicknames file", "file", filepath, "error", err)
		return disallowed
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue // skip empty lines and comments
		}

		nickname := strings.ToLower(line)
		if userutil.IsValidSubdomain(nickname) {
			disallowed[nickname] = true
		} else {
			logger.Warn("invalid nickname format in disallowed nicknames file",
				"file", filepath, "line", lineNum, "nickname", nickname)
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Error("error reading disallowed nicknames file", "file", filepath, "error", err)
	}

	logger.Info("loaded disallowed nicknames", "count", len(disallowed), "file", filepath)
	return disallowed
}

// isNicknameAllowed checks if a nickname is allowed (not in the disallowed list)
func (s *Signup) isNicknameAllowed(nickname string) bool {
	return !s.disallowedNicknames[strings.ToLower(nickname)]
}

func (s *Signup) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", s.signup)
	r.Post("/", s.signup)
	r.Get("/complete", s.complete)
	r.Post("/complete", s.complete)

	return r
}

func (s *Signup) signup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.pages.Signup(w)
	case http.MethodPost:
		if s.cf == nil {
			http.Error(w, "signup is disabled", http.StatusFailedDependency)
		}
		emailId := r.FormValue("email")

		noticeId := "signup-msg"
		if !email.IsValidEmail(emailId) {
			s.pages.Notice(w, noticeId, "Invalid email address.")
			return
		}

		exists, err := db.CheckEmailExistsAtAll(s.db, emailId)
		if err != nil {
			s.l.Error("failed to check email existence", "error", err)
			s.pages.Notice(w, noticeId, "Failed to complete signup. Try again later.")
			return
		}
		if exists {
			s.pages.Notice(w, noticeId, "Email already exists.")
			return
		}

		code, err := s.inviteCodeRequest()
		if err != nil {
			s.l.Error("failed to create invite code", "error", err)
			s.pages.Notice(w, noticeId, "Failed to create invite code.")
			return
		}

		em := email.Email{
			APIKey:  s.config.Resend.ApiKey,
			From:    s.config.Resend.SentFrom,
			To:      emailId,
			Subject: "Verify your Tangled account",
			Text: `Copy and paste this code below to verify your account on Tangled.
		` + code,
			Html: `<p>Copy and paste this code below to verify your account on Tangled.</p>
<p><code>` + code + `</code></p>`,
		}

		err = email.SendEmail(em)
		if err != nil {
			s.l.Error("failed to send email", "error", err)
			s.pages.Notice(w, noticeId, "Failed to send email.")
			return
		}
		err = db.AddInflightSignup(s.db, db.InflightSignup{
			Email:      emailId,
			InviteCode: code,
		})
		if err != nil {
			s.l.Error("failed to add inflight signup", "error", err)
			s.pages.Notice(w, noticeId, "Failed to complete sign up. Try again later.")
			return
		}

		s.pages.HxRedirect(w, "/signup/complete")
	}
}

func (s *Signup) complete(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.pages.CompleteSignup(w)
	case http.MethodPost:
		username := r.FormValue("username")
		password := r.FormValue("password")
		code := r.FormValue("code")

		if !userutil.IsValidSubdomain(username) {
			s.pages.Notice(w, "signup-error", "Invalid username. Username must be 4–63 characters, lowercase letters, digits, or hyphens, and can't start or end with a hyphen.")
			return
		}

		if !s.isNicknameAllowed(username) {
			s.pages.Notice(w, "signup-error", "This username is not available. Please choose a different one.")
			return
		}

		email, err := db.GetEmailForCode(s.db, code)
		if err != nil {
			s.l.Error("failed to get email for code", "error", err)
			s.pages.Notice(w, "signup-error", "Failed to complete sign up. Try again later.")
			return
		}

		did, err := s.createAccountRequest(username, password, email, code)
		if err != nil {
			s.l.Error("failed to create account", "error", err)
			s.pages.Notice(w, "signup-error", err.Error())
			return
		}

		if s.cf == nil {
			s.l.Error("cloudflare client is nil", "error", "Cloudflare integration is not enabled in configuration")
			s.pages.Notice(w, "signup-error", "Account signup is currently disabled. DNS record creation is not available. Please contact support.")
			return
		}

		err = s.cf.CreateDNSRecord(r.Context(), dns.Record{
			Type:    "TXT",
			Name:    "_atproto." + username,
			Content: "did=" + did,
			TTL:     6400,
			Proxied: false,
		})
		if err != nil {
			s.l.Error("failed to create DNS record", "error", err)
			s.pages.Notice(w, "signup-error", "Failed to create DNS record for your handle. Please contact support.")
			return
		}

		err = db.AddEmail(s.db, db.Email{
			Did:      did,
			Address:  email,
			Verified: true,
			Primary:  true,
		})
		if err != nil {
			s.l.Error("failed to add email", "error", err)
			s.pages.Notice(w, "signup-error", "Failed to complete sign up. Try again later.")
			return
		}

		s.pages.Notice(w, "signup-msg", fmt.Sprintf(`Account created successfully. You can now
			<a class="underline text-black dark:text-white" href="/login">login</a>
			with <code>%s.tngl.sh</code>.`, username))

		go func() {
			err := db.DeleteInflightSignup(s.db, email)
			if err != nil {
				s.l.Error("failed to delete inflight signup", "error", err)
			}
		}()
		return
	}
}
