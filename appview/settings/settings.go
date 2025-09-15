package settings

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/email"
	"tangled.org/core/appview/middleware"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/tid"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/gliderlabs/ssh"
	"github.com/google/uuid"
)

type Settings struct {
	Db     *db.DB
	OAuth  *oauth.OAuth
	Pages  *pages.Pages
	Config *config.Config
}

type tab = map[string]any

var (
	settingsTabs []tab = []tab{
		{"Name": "profile", "Icon": "user"},
		{"Name": "keys", "Icon": "key"},
		{"Name": "emails", "Icon": "mail"},
		{"Name": "notifications", "Icon": "bell"},
	}
)

func (s *Settings) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.AuthMiddleware(s.OAuth))

	// settings pages
	r.Get("/", s.profileSettings)
	r.Get("/profile", s.profileSettings)

	r.Route("/keys", func(r chi.Router) {
		r.Get("/", s.keysSettings)
		r.Put("/", s.keys)
		r.Delete("/", s.keys)
	})

	r.Route("/emails", func(r chi.Router) {
		r.Get("/", s.emailsSettings)
		r.Put("/", s.emails)
		r.Delete("/", s.emails)
		r.Get("/verify", s.emailsVerify)
		r.Post("/verify/resend", s.emailsVerifyResend)
		r.Post("/primary", s.emailsPrimary)
	})

	r.Route("/notifications", func(r chi.Router) {
		r.Get("/", s.notificationsSettings)
		r.Put("/", s.updateNotificationPreferences)
	})

	return r
}

func (s *Settings) profileSettings(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)

	s.Pages.UserProfileSettings(w, pages.UserProfileSettingsParams{
		LoggedInUser: user,
		Tabs:         settingsTabs,
		Tab:          "profile",
	})
}

func (s *Settings) notificationsSettings(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	did := s.OAuth.GetDid(r)

	prefs, err := s.Db.GetNotificationPreferences(r.Context(), did)
	if err != nil {
		log.Printf("failed to get notification preferences: %s", err)
		s.Pages.Notice(w, "settings-notifications-error", "Unable to load notification preferences.")
		return
	}

	s.Pages.UserNotificationSettings(w, pages.UserNotificationSettingsParams{
		LoggedInUser: user,
		Preferences:  prefs,
		Tabs:         settingsTabs,
		Tab:          "notifications",
	})
}

func (s *Settings) updateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	did := s.OAuth.GetDid(r)

	prefs := &models.NotificationPreferences{
		UserDid:            did,
		RepoStarred:        r.FormValue("repo_starred") == "on",
		IssueCreated:       r.FormValue("issue_created") == "on",
		IssueCommented:     r.FormValue("issue_commented") == "on",
		IssueClosed:        r.FormValue("issue_closed") == "on",
		PullCreated:        r.FormValue("pull_created") == "on",
		PullCommented:      r.FormValue("pull_commented") == "on",
		PullMerged:         r.FormValue("pull_merged") == "on",
		Followed:           r.FormValue("followed") == "on",
		EmailNotifications: r.FormValue("email_notifications") == "on",
	}

	err := s.Db.UpdateNotificationPreferences(r.Context(), prefs)
	if err != nil {
		log.Printf("failed to update notification preferences: %s", err)
		s.Pages.Notice(w, "settings-notifications-error", "Unable to save notification preferences.")
		return
	}

	s.Pages.Notice(w, "settings-notifications-success", "Notification preferences saved successfully.")
}

func (s *Settings) keysSettings(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	pubKeys, err := db.GetPublicKeysForDid(s.Db, user.Did)
	if err != nil {
		log.Println(err)
	}

	s.Pages.UserKeysSettings(w, pages.UserKeysSettingsParams{
		LoggedInUser: user,
		PubKeys:      pubKeys,
		Tabs:         settingsTabs,
		Tab:          "keys",
	})
}

func (s *Settings) emailsSettings(w http.ResponseWriter, r *http.Request) {
	user := s.OAuth.GetUser(r)
	emails, err := db.GetAllEmails(s.Db, user.Did)
	if err != nil {
		log.Println(err)
	}

	s.Pages.UserEmailsSettings(w, pages.UserEmailsSettingsParams{
		LoggedInUser: user,
		Emails:       emails,
		Tabs:         settingsTabs,
		Tab:          "emails",
	})
}

// buildVerificationEmail creates an email.Email struct for verification emails
func (s *Settings) buildVerificationEmail(emailAddr, did, code string) email.Email {
	verifyURL := s.verifyUrl(did, emailAddr, code)

	return email.Email{
		APIKey:  s.Config.Resend.ApiKey,
		From:    s.Config.Resend.SentFrom,
		To:      emailAddr,
		Subject: "Verify your Tangled email",
		Text: `Click the link below (or copy and paste it into your browser) to verify your email address.
` + verifyURL,
		Html: `<p>Click the link (or copy and paste it into your browser) to verify your email address.</p>
<p><a href="` + verifyURL + `">` + verifyURL + `</a></p>`,
	}
}

// sendVerificationEmail handles the common logic for sending verification emails
func (s *Settings) sendVerificationEmail(w http.ResponseWriter, did, emailAddr, code string, errorContext string) error {
	emailToSend := s.buildVerificationEmail(emailAddr, did, code)

	err := email.SendEmail(emailToSend)
	if err != nil {
		log.Printf("sending email: %s", err)
		s.Pages.Notice(w, "settings-emails-error", fmt.Sprintf("Unable to send verification email at this moment, try again later. %s", errorContext))
		return err
	}

	return nil
}

func (s *Settings) emails(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.Pages.Notice(w, "settings-emails", "Unimplemented.")
		log.Println("unimplemented")
		return
	case http.MethodPut:
		did := s.OAuth.GetDid(r)
		emAddr := r.FormValue("email")
		emAddr = strings.TrimSpace(emAddr)

		if !email.IsValidEmail(emAddr) {
			s.Pages.Notice(w, "settings-emails-error", "Invalid email address.")
			return
		}

		// check if email already exists in database
		existingEmail, err := db.GetEmail(s.Db, did, emAddr)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("checking for existing email: %s", err)
			s.Pages.Notice(w, "settings-emails-error", "Unable to add email at this moment, try again later.")
			return
		}

		if err == nil {
			if existingEmail.Verified {
				s.Pages.Notice(w, "settings-emails-error", "This email is already verified.")
				return
			}

			s.Pages.Notice(w, "settings-emails-error", "This email is already added but not verified. Check your inbox for the verification link.")
			return
		}

		code := uuid.New().String()

		// Begin transaction
		tx, err := s.Db.Begin()
		if err != nil {
			log.Printf("failed to start transaction: %s", err)
			s.Pages.Notice(w, "settings-emails-error", "Unable to add email at this moment, try again later.")
			return
		}
		defer tx.Rollback()

		if err := db.AddEmail(tx, models.Email{
			Did:              did,
			Address:          emAddr,
			Verified:         false,
			VerificationCode: code,
		}); err != nil {
			log.Printf("adding email: %s", err)
			s.Pages.Notice(w, "settings-emails-error", "Unable to add email at this moment, try again later.")
			return
		}

		if err := s.sendVerificationEmail(w, did, emAddr, code, ""); err != nil {
			return
		}

		// Commit transaction
		if err := tx.Commit(); err != nil {
			log.Printf("failed to commit transaction: %s", err)
			s.Pages.Notice(w, "settings-emails-error", "Unable to add email at this moment, try again later.")
			return
		}

		s.Pages.Notice(w, "settings-emails-success", "Click the link in the email we sent you to verify your email address.")
		return
	case http.MethodDelete:
		did := s.OAuth.GetDid(r)
		emailAddr := r.FormValue("email")
		emailAddr = strings.TrimSpace(emailAddr)

		// Begin transaction
		tx, err := s.Db.Begin()
		if err != nil {
			log.Printf("failed to start transaction: %s", err)
			s.Pages.Notice(w, "settings-emails-error", "Unable to delete email at this moment, try again later.")
			return
		}
		defer tx.Rollback()

		if err := db.DeleteEmail(tx, did, emailAddr); err != nil {
			log.Printf("deleting email: %s", err)
			s.Pages.Notice(w, "settings-emails-error", "Unable to delete email at this moment, try again later.")
			return
		}

		// Commit transaction
		if err := tx.Commit(); err != nil {
			log.Printf("failed to commit transaction: %s", err)
			s.Pages.Notice(w, "settings-emails-error", "Unable to delete email at this moment, try again later.")
			return
		}

		s.Pages.HxLocation(w, "/settings/emails")
		return
	}
}

func (s *Settings) verifyUrl(did string, email string, code string) string {
	var appUrl string
	if s.Config.Core.Dev {
		appUrl = "http://" + s.Config.Core.ListenAddr
	} else {
		appUrl = s.Config.Core.AppviewHost
	}

	return fmt.Sprintf("%s/settings/emails/verify?did=%s&email=%s&code=%s", appUrl, url.QueryEscape(did), url.QueryEscape(email), url.QueryEscape(code))
}

func (s *Settings) emailsVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Get the parameters directly from the query
	emailAddr := q.Get("email")
	did := q.Get("did")
	code := q.Get("code")

	valid, err := db.CheckValidVerificationCode(s.Db, did, emailAddr, code)
	if err != nil {
		log.Printf("checking email verification: %s", err)
		s.Pages.Notice(w, "settings-emails-error", "Error verifying email. Please try again later.")
		return
	}

	if !valid {
		s.Pages.Notice(w, "settings-emails-error", "Invalid verification code. Please request a new verification email.")
		return
	}

	// Mark email as verified in the database
	if err := db.MarkEmailVerified(s.Db, did, emailAddr); err != nil {
		log.Printf("marking email as verified: %s", err)
		s.Pages.Notice(w, "settings-emails-error", "Error updating email verification status. Please try again later.")
		return
	}

	http.Redirect(w, r, "/settings/emails", http.StatusSeeOther)
}

func (s *Settings) emailsVerifyResend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.Pages.Notice(w, "settings-emails-error", "Invalid request method.")
		return
	}

	did := s.OAuth.GetDid(r)
	emAddr := r.FormValue("email")
	emAddr = strings.TrimSpace(emAddr)

	if !email.IsValidEmail(emAddr) {
		s.Pages.Notice(w, "settings-emails-error", "Invalid email address.")
		return
	}

	// Check if email exists and is unverified
	existingEmail, err := db.GetEmail(s.Db, did, emAddr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.Pages.Notice(w, "settings-emails-error", "Email not found. Please add it first.")
		} else {
			log.Printf("checking for existing email: %s", err)
			s.Pages.Notice(w, "settings-emails-error", "Unable to resend verification email at this moment, try again later.")
		}
		return
	}

	if existingEmail.Verified {
		s.Pages.Notice(w, "settings-emails-error", "This email is already verified.")
		return
	}

	// Check if last verification email was sent less than 10 minutes ago
	if existingEmail.LastSent != nil {
		timeSinceLastSent := time.Since(*existingEmail.LastSent)
		if timeSinceLastSent < 10*time.Minute {
			waitTime := 10*time.Minute - timeSinceLastSent
			s.Pages.Notice(w, "settings-emails-error", fmt.Sprintf("Please wait %d minutes before requesting another verification email.", int(waitTime.Minutes()+1)))
			return
		}
	}

	// Generate new verification code
	code := uuid.New().String()

	// Begin transaction
	tx, err := s.Db.Begin()
	if err != nil {
		log.Printf("failed to start transaction: %s", err)
		s.Pages.Notice(w, "settings-emails-error", "Unable to resend verification email at this moment, try again later.")
		return
	}
	defer tx.Rollback()

	// Update the verification code and last sent time
	if err := db.UpdateVerificationCode(tx, did, emAddr, code); err != nil {
		log.Printf("updating email verification: %s", err)
		s.Pages.Notice(w, "settings-emails-error", "Unable to resend verification email at this moment, try again later.")
		return
	}

	// Send verification email
	if err := s.sendVerificationEmail(w, did, emAddr, code, ""); err != nil {
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		log.Printf("failed to commit transaction: %s", err)
		s.Pages.Notice(w, "settings-emails-error", "Unable to resend verification email at this moment, try again later.")
		return
	}

	s.Pages.Notice(w, "settings-emails-success", "Verification email resent. Click the link in the email we sent you to verify your email address.")
}

func (s *Settings) emailsPrimary(w http.ResponseWriter, r *http.Request) {
	did := s.OAuth.GetDid(r)
	emailAddr := r.FormValue("email")
	emailAddr = strings.TrimSpace(emailAddr)

	if emailAddr == "" {
		s.Pages.Notice(w, "settings-emails-error", "Email address cannot be empty.")
		return
	}

	if err := db.MakeEmailPrimary(s.Db, did, emailAddr); err != nil {
		log.Printf("setting primary email: %s", err)
		s.Pages.Notice(w, "settings-emails-error", "Error setting primary email. Please try again later.")
		return
	}

	s.Pages.HxLocation(w, "/settings/emails")
}

func (s *Settings) keys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.Pages.Notice(w, "settings-keys", "Unimplemented.")
		log.Println("unimplemented")
		return
	case http.MethodPut:
		did := s.OAuth.GetDid(r)
		key := r.FormValue("key")
		key = strings.TrimSpace(key)
		name := r.FormValue("name")
		client, err := s.OAuth.AuthorizedClient(r)
		if err != nil {
			s.Pages.Notice(w, "settings-keys", "Failed to authorize. Try again later.")
			return
		}

		_, _, _, _, err = ssh.ParseAuthorizedKey([]byte(key))
		if err != nil {
			log.Printf("parsing public key: %s", err)
			s.Pages.Notice(w, "settings-keys", "That doesn't look like a valid public key. Make sure it's a <strong>public</strong> key.")
			return
		}

		rkey := tid.TID()

		tx, err := s.Db.Begin()
		if err != nil {
			log.Printf("failed to start tx; adding public key: %s", err)
			s.Pages.Notice(w, "settings-keys", "Unable to add public key at this moment, try again later.")
			return
		}
		defer tx.Rollback()

		if err := db.AddPublicKey(tx, did, name, key, rkey); err != nil {
			log.Printf("adding public key: %s", err)
			s.Pages.Notice(w, "settings-keys", "Failed to add public key.")
			return
		}

		// store in pds too
		resp, err := client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.PublicKeyNSID,
			Repo:       did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.PublicKey{
					CreatedAt: time.Now().Format(time.RFC3339),
					Key:       key,
					Name:      name,
				}},
		})
		// invalid record
		if err != nil {
			log.Printf("failed to create record: %s", err)
			s.Pages.Notice(w, "settings-keys", "Failed to create record.")
			return
		}

		log.Println("created atproto record: ", resp.Uri)

		err = tx.Commit()
		if err != nil {
			log.Printf("failed to commit tx; adding public key: %s", err)
			s.Pages.Notice(w, "settings-keys", "Unable to add public key at this moment, try again later.")
			return
		}

		s.Pages.HxLocation(w, "/settings/keys")
		return

	case http.MethodDelete:
		did := s.OAuth.GetDid(r)
		q := r.URL.Query()

		name := q.Get("name")
		rkey := q.Get("rkey")
		key := q.Get("key")

		log.Println(name)
		log.Println(rkey)
		log.Println(key)

		client, err := s.OAuth.AuthorizedClient(r)
		if err != nil {
			log.Printf("failed to authorize client: %s", err)
			s.Pages.Notice(w, "settings-keys", "Failed to authorize client.")
			return
		}

		if err := db.DeletePublicKey(s.Db, did, name, key); err != nil {
			log.Printf("removing public key: %s", err)
			s.Pages.Notice(w, "settings-keys", "Failed to remove public key.")
			return
		}

		if rkey != "" {
			// remove from pds too
			_, err := client.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
				Collection: tangled.PublicKeyNSID,
				Repo:       did,
				Rkey:       rkey,
			})

			// invalid record
			if err != nil {
				log.Printf("failed to delete record from PDS: %s", err)
				s.Pages.Notice(w, "settings-keys", "Failed to remove key from PDS.")
				return
			}
		}
		log.Println("deleted successfully")

		s.Pages.HxLocation(w, "/settings/keys")
		return
	}
}
