package issues

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview/middleware"
)

func (i *Issues) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.Route("/", func(r chi.Router) {
		r.With(middleware.Paginate).Get("/", i.RepoIssues)

		r.Route("/{issue}", func(r chi.Router) {
			r.Use(mw.ResolveIssue)
			r.Get("/", i.RepoSingleIssue)

			// authenticated routes
			r.Group(func(r chi.Router) {
				r.Use(middleware.AuthMiddleware(i.oauth))
				r.Post("/comment", i.NewIssueComment)
				r.Route("/comment/{commentId}/", func(r chi.Router) {
					r.Get("/", i.IssueComment)
					r.Delete("/", i.DeleteIssueComment)
					r.Get("/edit", i.EditIssueComment)
					r.Post("/edit", i.EditIssueComment)
					r.Get("/reply", i.ReplyIssueComment)
					r.Get("/replyPlaceholder", i.ReplyIssueCommentPlaceholder)
				})
				r.Get("/edit", i.EditIssue)
				r.Post("/edit", i.EditIssue)
				r.Delete("/", i.DeleteIssue)
				r.Post("/close", i.CloseIssue)
				r.Post("/reopen", i.ReopenIssue)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.AuthMiddleware(i.oauth))
			r.Get("/new", i.NewIssue)
			r.Post("/new", i.NewIssue)
		})
	})

	return r
}
