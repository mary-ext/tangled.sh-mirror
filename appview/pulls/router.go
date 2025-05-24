package pulls

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview/middleware"
)

func (s *Pulls) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()
	r.Get("/", s.RepoPulls)
	r.With(middleware.AuthMiddleware(s.oauth)).Route("/new", func(r chi.Router) {
		r.Get("/", s.NewPull)
		r.Get("/patch-upload", s.PatchUploadFragment)
		r.Post("/validate-patch", s.ValidatePatch)
		r.Get("/compare-branches", s.CompareBranchesFragment)
		r.Get("/compare-forks", s.CompareForksFragment)
		r.Get("/fork-branches", s.CompareForksBranchesFragment)
		r.Post("/", s.NewPull)
	})

	r.Route("/{pull}", func(r chi.Router) {
		r.Use(mw.ResolvePull())
		r.Get("/", s.RepoSinglePull)

		r.Route("/round/{round}", func(r chi.Router) {
			r.Get("/", s.RepoPullPatch)
			r.Get("/interdiff", s.RepoPullInterdiff)
			r.Get("/actions", s.PullActions)
			r.With(middleware.AuthMiddleware(s.oauth)).Route("/comment", func(r chi.Router) {
				r.Get("/", s.PullComment)
				r.Post("/", s.PullComment)
			})
		})

		r.Route("/round/{round}.patch", func(r chi.Router) {
			r.Get("/", s.RepoPullPatchRaw)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.AuthMiddleware(s.oauth))
			r.Route("/resubmit", func(r chi.Router) {
				r.Get("/", s.ResubmitPull)
				r.Post("/", s.ResubmitPull)
			})
			r.Post("/close", s.ClosePull)
			r.Post("/reopen", s.ReopenPull)
			// collaborators only
			r.Group(func(r chi.Router) {
				r.Use(mw.RepoPermissionMiddleware("repo:push"))
				r.Post("/merge", s.MergePull)
				// maybe lock, etc.
			})
		})
	})
	return r

}
