package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview"
	"tangled.org/core/appview/cache"
	"tangled.org/core/appview/cache/session"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	dbnotify "tangled.org/core/appview/notify/db"
	phnotify "tangled.org/core/appview/notify/posthog"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/reporesolver"
	"tangled.org/core/appview/validator"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/eventconsumer"
	"tangled.org/core/idresolver"
	"tangled.org/core/jetstream"
	tlog "tangled.org/core/log"
	"tangled.org/core/rbac"
	"tangled.org/core/tid"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	atpclient "github.com/bluesky-social/indigo/atproto/client"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/posthog/posthog-go"
)

type State struct {
	db            *db.DB
	notifier      notify.Notifier
	oauth         *oauth.OAuth
	enforcer      *rbac.Enforcer
	pages         *pages.Pages
	sess          *session.SessionStore
	idResolver    *idresolver.Resolver
	posthog       posthog.Client
	jc            *jetstream.JetstreamClient
	config        *config.Config
	repoResolver  *reporesolver.RepoResolver
	knotstream    *eventconsumer.Consumer
	spindlestream *eventconsumer.Consumer
	logger        *slog.Logger
	validator     *validator.Validator
}

func Make(ctx context.Context, config *config.Config) (*State, error) {
	d, err := db.Make(config.Core.DbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create db: %w", err)
	}

	enforcer, err := rbac.NewEnforcer(config.Core.DbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create enforcer: %w", err)
	}

	res, err := idresolver.RedisResolver(config.Redis.ToURL())
	if err != nil {
		log.Printf("failed to create redis resolver: %v", err)
		res = idresolver.DefaultResolver()
	}

	pages := pages.NewPages(config, res)
	cache := cache.New(config.Redis.Addr)
	sess := session.New(cache)
	oauth2, err := oauth.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to start oauth handler: %w", err)
	}
	validator := validator.New(d, res, enforcer)

	posthog, err := posthog.NewWithConfig(config.Posthog.ApiKey, posthog.Config{Endpoint: config.Posthog.Endpoint})
	if err != nil {
		return nil, fmt.Errorf("failed to create posthog client: %w", err)
	}

	repoResolver := reporesolver.New(config, enforcer, res, d)

	wrapper := db.DbWrapper{Execer: d}
	jc, err := jetstream.NewJetstreamClient(
		config.Jetstream.Endpoint,
		"appview",
		[]string{
			tangled.GraphFollowNSID,
			tangled.FeedStarNSID,
			tangled.PublicKeyNSID,
			tangled.RepoArtifactNSID,
			tangled.ActorProfileNSID,
			tangled.SpindleMemberNSID,
			tangled.SpindleNSID,
			tangled.StringNSID,
			tangled.RepoIssueNSID,
			tangled.RepoIssueCommentNSID,
			tangled.LabelDefinitionNSID,
			tangled.LabelOpNSID,
		},
		nil,
		slog.Default(),
		wrapper,
		false,

		// in-memory filter is inapplicalble to appview so
		// we'll never log dids anyway.
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create jetstream client: %w", err)
	}

	if err := BackfillDefaultDefs(d, res); err != nil {
		return nil, fmt.Errorf("failed to backfill default label defs: %w", err)
	}

	ingester := appview.Ingester{
		Db:         wrapper,
		Enforcer:   enforcer,
		IdResolver: res,
		Config:     config,
		Logger:     tlog.New("ingester"),
		Validator:  validator,
	}
	err = jc.StartJetstream(ctx, ingester.Ingest())
	if err != nil {
		return nil, fmt.Errorf("failed to start jetstream watcher: %w", err)
	}

	knotstream, err := Knotstream(ctx, config, d, enforcer, posthog)
	if err != nil {
		return nil, fmt.Errorf("failed to start knotstream consumer: %w", err)
	}
	knotstream.Start(ctx)

	spindlestream, err := Spindlestream(ctx, config, d, enforcer)
	if err != nil {
		return nil, fmt.Errorf("failed to start spindlestream consumer: %w", err)
	}
	spindlestream.Start(ctx)

	var notifiers []notify.Notifier

	// Always add the database notifier
	notifiers = append(notifiers, dbnotify.NewDatabaseNotifier(d, res))

	// Add other notifiers in production only
	if !config.Core.Dev {
		notifiers = append(notifiers, phnotify.NewPosthogNotifier(posthog))
	}
	notifier := notify.NewMergedNotifier(notifiers...)

	state := &State{
		d,
		notifier,
		oauth2,
		enforcer,
		pages,
		sess,
		res,
		posthog,
		jc,
		config,
		repoResolver,
		knotstream,
		spindlestream,
		slog.Default(),
		validator,
	}

	return state, nil
}

func (s *State) Close() error {
	// other close up logic goes here
	return s.db.Close()
}

func (s *State) Favicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=31536000") // one year
	w.Header().Set("ETag", `"favicon-svg-v1"`)

	if match := r.Header.Get("If-None-Match"); match == `"favicon-svg-v1"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	s.pages.Favicon(w)
}

func (s *State) RobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "public, max-age=86400") // one day

	robotsTxt := `User-agent: *
Allow: /
`
	w.Write([]byte(robotsTxt))
}

// https://developer.mozilla.org/en-US/docs/Web/Progressive_web_apps/Manifest
const manifestJson = `{
  "name": "tangled",
  "description": "tightly-knit social coding.",
  "icons": [
    {
      "src": "/favicon.svg",
      "sizes": "144x144"
    }
  ],
  "start_url": "/",
  "id": "org.tangled",

  "display": "standalone",
  "background_color": "#111827",
  "theme_color": "#111827"
}`

func (p *State) PWAManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(manifestJson))
}

func (s *State) TermsOfService(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	s.pages.TermsOfService(w, pages.TermsOfServiceParams{
		LoggedInUser: user,
	})
}

func (s *State) PrivacyPolicy(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	s.pages.PrivacyPolicy(w, pages.PrivacyPolicyParams{
		LoggedInUser: user,
	})
}

func (s *State) Brand(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	s.pages.Brand(w, pages.BrandParams{
		LoggedInUser: user,
	})
}

func (s *State) HomeOrTimeline(w http.ResponseWriter, r *http.Request) {
	if s.oauth.GetUser(r) != nil {
		s.Timeline(w, r)
		return
	}
	s.Home(w, r)
}

func (s *State) Timeline(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	// TODO: set this flag based on the UI
	filtered := false

	var userDid string
	if user != nil {
		userDid = user.Did
	}
	timeline, err := db.MakeTimeline(s.db, 50, userDid, filtered)
	if err != nil {
		log.Println(err)
		s.pages.Notice(w, "timeline", "Uh oh! Failed to load timeline.")
	}

	repos, err := db.GetTopStarredReposLastWeek(s.db)
	if err != nil {
		log.Println(err)
		s.pages.Notice(w, "topstarredrepos", "Unable to load.")
		return
	}

	gfiLabel, err := db.GetLabelDefinition(s.db, db.FilterEq("at_uri", models.LabelGoodFirstIssue))
	if err != nil {
		// non-fatal
	}

	s.pages.Timeline(w, pages.TimelineParams{
		LoggedInUser: user,
		Timeline:     timeline,
		Repos:        repos,
		GfiLabel:     gfiLabel,
	})
}

func (s *State) UpgradeBanner(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	if user == nil {
		return
	}

	l := s.logger.With("handler", "UpgradeBanner")
	l = l.With("did", user.Did)

	regs, err := db.GetRegistrations(
		s.db,
		db.FilterEq("did", user.Did),
		db.FilterEq("needs_upgrade", 1),
	)
	if err != nil {
		l.Error("non-fatal: failed to get registrations", "err", err)
	}

	spindles, err := db.GetSpindles(
		s.db,
		db.FilterEq("owner", user.Did),
		db.FilterEq("needs_upgrade", 1),
	)
	if err != nil {
		l.Error("non-fatal: failed to get spindles", "err", err)
	}

	if regs == nil && spindles == nil {
		return
	}

	s.pages.UpgradeBanner(w, pages.UpgradeBannerParams{
		Registrations: regs,
		Spindles:      spindles,
	})
}

func (s *State) Home(w http.ResponseWriter, r *http.Request) {
	// TODO: set this flag based on the UI
	filtered := false

	timeline, err := db.MakeTimeline(s.db, 5, "", filtered)
	if err != nil {
		log.Println(err)
		s.pages.Notice(w, "timeline", "Uh oh! Failed to load timeline.")
		return
	}

	repos, err := db.GetTopStarredReposLastWeek(s.db)
	if err != nil {
		log.Println(err)
		s.pages.Notice(w, "topstarredrepos", "Unable to load.")
		return
	}

	s.pages.Home(w, pages.TimelineParams{
		LoggedInUser: nil,
		Timeline:     timeline,
		Repos:        repos,
	})
}

func (s *State) Keys(w http.ResponseWriter, r *http.Request) {
	user := chi.URLParam(r, "user")
	user = strings.TrimPrefix(user, "@")

	if user == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	id, err := s.idResolver.ResolveIdent(r.Context(), user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	pubKeys, err := db.GetPublicKeysForDid(s.db, id.DID.String())
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if len(pubKeys) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	for _, k := range pubKeys {
		key := strings.TrimRight(k.Key, "\n")
		fmt.Fprintln(w, key)
	}
}

func validateRepoName(name string) error {
	// check for path traversal attempts
	if name == "." || name == ".." ||
		strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("Repository name contains invalid path characters")
	}

	// check for sequences that could be used for traversal when normalized
	if strings.Contains(name, "./") || strings.Contains(name, "../") ||
		strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("Repository name contains invalid path sequence")
	}

	// then continue with character validation
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == '.') {
			return fmt.Errorf("Repository name can only contain alphanumeric characters, periods, hyphens, and underscores")
		}
	}

	// additional check to prevent multiple sequential dots
	if strings.Contains(name, "..") {
		return fmt.Errorf("Repository name cannot contain sequential dots")
	}

	// if all checks pass
	return nil
}

func stripGitExt(name string) string {
	return strings.TrimSuffix(name, ".git")
}

func (s *State) NewRepo(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user := s.oauth.GetUser(r)
		knots, err := s.enforcer.GetKnotsForUser(user.Did)
		if err != nil {
			s.pages.Notice(w, "repo", "Invalid user account.")
			return
		}

		s.pages.NewRepo(w, pages.NewRepoParams{
			LoggedInUser: user,
			Knots:        knots,
		})

	case http.MethodPost:
		l := s.logger.With("handler", "NewRepo")

		user := s.oauth.GetUser(r)
		l = l.With("did", user.Did)

		// form validation
		domain := r.FormValue("domain")
		if domain == "" {
			s.pages.Notice(w, "repo", "Invalid form submission&mdash;missing knot domain.")
			return
		}
		l = l.With("knot", domain)

		repoName := r.FormValue("name")
		if repoName == "" {
			s.pages.Notice(w, "repo", "Repository name cannot be empty.")
			return
		}

		if err := validateRepoName(repoName); err != nil {
			s.pages.Notice(w, "repo", err.Error())
			return
		}
		repoName = stripGitExt(repoName)
		l = l.With("repoName", repoName)

		defaultBranch := r.FormValue("branch")
		if defaultBranch == "" {
			defaultBranch = "main"
		}
		l = l.With("defaultBranch", defaultBranch)

		description := r.FormValue("description")

		// ACL validation
		ok, err := s.enforcer.E.Enforce(user.Did, domain, domain, "repo:create")
		if err != nil || !ok {
			l.Info("unauthorized")
			s.pages.Notice(w, "repo", "You do not have permission to create a repo in this knot.")
			return
		}

		// Check for existing repos
		existingRepo, err := db.GetRepo(
			s.db,
			db.FilterEq("did", user.Did),
			db.FilterEq("name", repoName),
		)
		if err == nil && existingRepo != nil {
			l.Info("repo exists")
			s.pages.Notice(w, "repo", fmt.Sprintf("You already have a repository by this name on %s", existingRepo.Knot))
			return
		}

		// create atproto record for this repo
		rkey := tid.TID()
		repo := &models.Repo{
			Did:         user.Did,
			Name:        repoName,
			Knot:        domain,
			Rkey:        rkey,
			Description: description,
			Created:     time.Now(),
			Labels:      models.DefaultLabelDefs(),
		}
		record := repo.AsRecord()

		atpClient, err := s.oauth.AuthorizedClient(r)
		if err != nil {
			l.Info("PDS write failed", "err", err)
			s.pages.Notice(w, "repo", "Failed to write record to PDS.")
			return
		}

		atresp, err := comatproto.RepoPutRecord(r.Context(), atpClient, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &record,
			},
		})
		if err != nil {
			l.Info("PDS write failed", "err", err)
			s.pages.Notice(w, "repo", "Failed to announce repository creation.")
			return
		}

		aturi := atresp.Uri
		l = l.With("aturi", aturi)
		l.Info("wrote to PDS")

		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			l.Info("txn failed", "err", err)
			s.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// The rollback function reverts a few things on failure:
		// - the pending txn
		// - the ACLs
		// - the atproto record created
		rollback := func() {
			err1 := tx.Rollback()
			err2 := s.enforcer.E.LoadPolicy()
			err3 := rollbackRecord(context.Background(), aturi, atpClient)

			// ignore txn complete errors, this is okay
			if errors.Is(err1, sql.ErrTxDone) {
				err1 = nil
			}

			if errs := errors.Join(err1, err2, err3); errs != nil {
				l.Error("failed to rollback changes", "errs", errs)
				return
			}
		}
		defer rollback()

		client, err := s.oauth.ServiceClient(
			r,
			oauth.WithService(domain),
			oauth.WithLxm(tangled.RepoCreateNSID),
			oauth.WithDev(s.config.Core.Dev),
		)
		if err != nil {
			l.Error("service auth failed", "err", err)
			s.pages.Notice(w, "repo", "Failed to reach PDS.")
			return
		}

		xe := tangled.RepoCreate(
			r.Context(),
			client,
			&tangled.RepoCreate_Input{
				Rkey: rkey,
			},
		)
		if err := xrpcclient.HandleXrpcErr(xe); err != nil {
			l.Error("xrpc error", "xe", xe)
			s.pages.Notice(w, "repo", err.Error())
			return
		}

		err = db.AddRepo(tx, repo)
		if err != nil {
			l.Error("db write failed", "err", err)
			s.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// acls
		p, _ := securejoin.SecureJoin(user.Did, repoName)
		err = s.enforcer.AddRepo(user.Did, domain, p)
		if err != nil {
			l.Error("acl setup failed", "err", err)
			s.pages.Notice(w, "repo", "Failed to set up repository permissions.")
			return
		}

		err = tx.Commit()
		if err != nil {
			l.Error("txn commit failed", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = s.enforcer.E.SavePolicy()
		if err != nil {
			l.Error("acl save failed", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// reset the ATURI because the transaction completed successfully
		aturi = ""

		s.notifier.NewRepo(r.Context(), repo)
		s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s", user.Did, repoName))
	}
}

// this is used to rollback changes made to the PDS
//
// it is a no-op if the provided ATURI is empty
func rollbackRecord(ctx context.Context, aturi string, client *atpclient.APIClient) error {
	if aturi == "" {
		return nil
	}

	parsed := syntax.ATURI(aturi)

	collection := parsed.Collection().String()
	repo := parsed.Authority().String()
	rkey := parsed.RecordKey().String()

	_, err := comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
		Collection: collection,
		Repo:       repo,
		Rkey:       rkey,
	})
	return err
}

func BackfillDefaultDefs(e db.Execer, r *idresolver.Resolver) error {
	defaults := models.DefaultLabelDefs()
	defaultLabels, err := db.GetLabelDefinitions(e, db.FilterIn("at_uri", defaults))
	if err != nil {
		return err
	}
	// already present
	if len(defaultLabels) == len(defaults) {
		return nil
	}

	labelDefs, err := models.FetchDefaultDefs(r)
	if err != nil {
		return err
	}

	// Insert each label definition to the database
	for _, labelDef := range labelDefs {
		_, err = db.AddLabelDefinition(e, &labelDef)
		if err != nil {
			return fmt.Errorf("failed to add label definition %s: %v", labelDef.Name, err)
		}
	}

	return nil
}
