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
		r.Get("/{issue}", i.RepoSingleIssue)

		r.Group(func(r chi.Router) {
			r.Use(middleware.AuthMiddleware(i.oauth))
			r.Get("/new", i.NewIssue)
			r.Post("/new", i.NewIssue)
			r.Post("/{issue}/comment", i.NewIssueComment)
			r.Route("/{issue}/comment/{comment_id}/", func(r chi.Router) {
				r.Get("/", i.IssueComment)
				r.Delete("/", i.DeleteIssueComment)
				r.Get("/edit", i.EditIssueComment)
				r.Post("/edit", i.EditIssueComment)
			})
			r.Post("/{issue}/close", i.CloseIssue)
			r.Post("/{issue}/reopen", i.ReopenIssue)
		})
	})

	return r
}
