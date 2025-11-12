package web

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/refresolver"
	"tangled.org/core/appview/service/issue"
	"tangled.org/core/appview/web/handler"
	"tangled.org/core/appview/web/middleware"
	"tangled.org/core/idresolver"
)

// Rules
// - Use single function for each endpoints (unless it doesn't make sense.)
// - Name handler files following the related path (ancestor paths can be
//   trimmed.)
// - Uass dependencies to each handlers, don't create structs with shared
//   dependencies unless it serves some domain-specific roles like
//   service/issue. Same rule goes to middlewares.

func UserRouter(
	// NOTE: put base dependencies (db, idResolver, oauth etc)
	logger *slog.Logger,
	config *config.Config,
	db *db.DB,
	idResolver *idresolver.Resolver,
	refResolver *refresolver.Resolver,
	notifier notify.Notifier,
	oauth *oauth.OAuth,
	pages *pages.Pages,
) http.Handler {
	r := chi.NewRouter()

	auth := middleware.AuthMiddleware(oauth)

	issue := issue.NewService(
		logger,
		config,
		db,
		notifier,
		refResolver,
	)

	r.Use(middleware.WithLogger(logger))

	r.Route("/{user}", func(r chi.Router) {
		r.Use(middleware.ResolveIdent(idResolver, pages))

		// r.Get("/", Profile)
		// r.Get("/feed.atom", AtomFeedPage)

		r.Route("/{repo}", func(r chi.Router) {
			r.Use(middleware.ResolveRepo(db, idResolver, pages))

			// /{user}/{repo}/issues/*
			r.With(middleware.Paginate).Get("/issues", handler.RepoIssues(issue))
			r.With(auth).Get("/issues/new", handler.NewIssue(pages))
			r.With(auth).Post("/issues/new", handler.NewIssuePost(issue, pages))
			r.Route("/issues/{issue}", func(r chi.Router) {
				r.Use(middleware.ResolveIssue(db, idResolver, pages))

				r.Get("/", handler.Issue(issue))
				r.Get("/opengraph", handler.IssueOpenGraph(issue))

				r.With(auth).Delete("/", handler.IssueDelete(issue, pages))

				r.With(auth).Get("/edit", handler.IssueEdit(issue))
				r.With(auth).Post("/edit", handler.IssueEditPost(issue))

				r.With(auth).Post("/close", handler.CloseIssue(issue))
				r.With(auth).Post("/reopen", handler.ReopenIssue(issue))

				// TODO: comments
			})

			// TODO: put more routes
		})
	})

	return r
}
