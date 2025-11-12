package web

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/indexer"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	isvc "tangled.org/core/appview/service/issue"
	rsvc "tangled.org/core/appview/service/repo"
	"tangled.org/core/appview/state"
	"tangled.org/core/appview/validator"
	"tangled.org/core/appview/web/handler"
	"tangled.org/core/appview/web/middleware"
	"tangled.org/core/idresolver"
	"tangled.org/core/rbac"
)

// Rules
// - Use single function for each endpoints (unless it doesn't make sense.)
// - Name handler files following the related path (ancestor paths can be
//   trimmed.)
// - Pass dependencies to each handlers, don't create structs with shared
//   dependencies unless it serves some domain-specific roles like
//   service/issue. Same rule goes to middlewares.

// RouterFromState creates a web router from `state.State`. This exist to
// bridge between legacy web routers under `State` and new architecture
func RouterFromState(s *state.State) http.Handler {
	config, db, enforcer, idResolver, indexer, logger, notifier, oauth, pages, validator := s.Expose()

	return Router(
		logger,
		config,
		db,
		enforcer,
		idResolver,
		indexer,
		notifier,
		oauth,
		pages,
		validator,
		s,
	)
}

func Router(
	// NOTE: put base dependencies (db, idResolver, oauth etc)
	logger *slog.Logger,
	config *config.Config,
	db *db.DB,
	enforcer *rbac.Enforcer,
	idResolver *idresolver.Resolver,
	indexer *indexer.Indexer,
	notifier notify.Notifier,
	oauth *oauth.OAuth,
	pages *pages.Pages,
	validator *validator.Validator,
	// to use legacy web handlers. will be removed later
	s *state.State,
) http.Handler {
	repo := rsvc.NewService(
		logger,
		config,
		db,
		enforcer,
	)
	issue := isvc.NewService(
		logger,
		config,
		db,
		notifier,
		idResolver,
		indexer.Issues,
		validator,
	)

	i := s.ExposeIssue()

	r := chi.NewRouter()

	mw := s.Middleware()
	auth := middleware.AuthMiddleware()

	r.Use(middleware.WithLogger(logger))
	r.Use(middleware.WithSession(oauth))

	r.Use(middleware.Normalize())

	r.Get("/favicon.svg", s.Favicon)
	r.Get("/favicon.ico", s.Favicon)
	r.Get("/pwa-manifest.json", s.PWAManifest)
	r.Get("/robots.txt", s.RobotsTxt)

	r.Handle("/static/*", pages.Static())

	r.Get("/", s.HomeOrTimeline)
	r.Get("/timeline", s.Timeline)
	r.Get("/upgradeBanner", s.UpgradeBanner)

	r.Get("/terms", s.TermsOfService)
	r.Get("/privacy", s.PrivacyPolicy)
	r.Get("/brand", s.Brand)
	// special-case handler for serving tangled.org/core
	r.Get("/core", s.Core())

	r.Get("/login", s.Login)
	r.Post("/login", s.Login)
	r.Post("/logout", s.Logout)

	r.Get("/goodfirstissues", s.GoodFirstIssues)

	r.With(auth).Get("/repo/new", s.NewRepo)
	r.With(auth).Post("/repo/new", s.NewRepo)

	r.With(auth).Post("/follow", s.Follow)
	r.With(auth).Delete("/follow", s.Follow)

	r.With(auth).Post("/star", s.Star)
	r.With(auth).Delete("/star", s.Star)

	r.With(auth).Post("/react", s.React)
	r.With(auth).Delete("/react", s.React)

	r.With(auth).Get("/profile/edit-bio", s.EditBioFragment)
	r.With(auth).Get("/profile/edit-pins", s.EditPinsFragment)
	r.With(auth).Post("/profile/bio", s.UpdateProfileBio)
	r.With(auth).Post("/profile/pins", s.UpdateProfilePins)

	r.Mount("/settings", s.SettingsRouter())
	r.Mount("/strings", s.StringsRouter(mw))
	r.Mount("/knots", s.KnotsRouter())
	r.Mount("/spindles", s.SpindlesRouter())
	r.Mount("/notifications", s.NotificationsRouter(mw))

	r.Mount("/signup", s.SignupRouter())
	r.Get("/oauth/client-metadata.json", handler.OauthClientMetadata(oauth))
	r.Get("/oauth/jwks.json", handler.OauthJwks(oauth))
	r.Get("/oauth/callback", oauth.Callback)

	// special-case handler. should replace with xrpc later
	r.Get("/keys/{user}", s.Keys)

	r.HandleFunc("/@*", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+chi.URLParam(r, "*"), http.StatusFound)
	})

	r.Route("/{user}", func(r chi.Router) {
		r.Use(middleware.EnsureDidOrHandle(pages))
		r.Use(middleware.ResolveIdent(idResolver, pages))

		r.Get("/", s.Profile)
		r.Get("/feed.atom", s.AtomFeedPage)

		r.Route("/{repo}", func(r chi.Router) {
			r.Use(middleware.ResolveRepo(db, pages))

			r.Mount("/", s.RepoRouter(mw))

			// /{user}/{repo}/issues/*
			r.With(middleware.Paginate).Get("/issues", handler.RepoIssues(issue, repo, pages, db))
			r.With(auth).Get("/issues/new", handler.NewIssue(repo, pages))
			r.With(auth).Post("/issues/new", handler.NewIssuePost(issue, pages))
			r.Route("/issues/{issue}", func(r chi.Router) {
				r.Use(middleware.ResolveIssue(db, pages))

				r.Get("/", handler.Issue(issue, repo, pages))
				r.Get("/opengraph", i.IssueOpenGraphSummary)

				r.With(auth).Delete("/", handler.IssueDelete(issue, pages))

				r.With(auth).Get("/edit", handler.IssueEdit(issue, repo, pages))
				r.With(auth).Post("/edit", handler.IssueEditPost(issue, pages))

				// r.With(auth).Post("/close", handler.CloseIssue(issue))
				// r.With(auth).Post("/reopen", handler.ReopenIssue(issue))

				r.With(auth).Post("/close", i.CloseIssue)
				r.With(auth).Post("/reopen", i.ReopenIssue)

				r.With(auth).Post("/comment", i.NewIssueComment)
				r.With(auth).Route("/comment/{commentId}/", func(r chi.Router) {
					r.Get("/", i.IssueComment)
					r.Delete("/", i.DeleteIssueComment)
					r.Get("/edit", i.EditIssueComment)
					r.Post("/edit", i.EditIssueComment)
					r.Get("/reply", i.ReplyIssueComment)
					r.Get("/replyPlaceholder", i.ReplyIssueCommentPlaceholder)
				})
			})

			r.Mount("/pulls", s.PullsRouter(mw))
			r.Mount("/pipelines", s.PipelinesRouter())
			r.Mount("/labels", s.LabelsRouter())

			// These routes get proxied to the knot
			r.Get("/info/refs", s.InfoRefs)
			r.Post("/git-upload-pack", s.UploadPack)
			r.Post("/git-receive-pack", s.ReceivePack)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		pages.Error404(w)
	})

	return r
}
