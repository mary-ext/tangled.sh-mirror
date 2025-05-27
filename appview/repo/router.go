package repo

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview/middleware"
)

func (rp *Repo) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()
	r.Get("/", rp.RepoIndex)
	r.Get("/commits/{ref}", rp.RepoLog)
	r.Route("/tree/{ref}", func(r chi.Router) {
		r.Get("/", rp.RepoIndex)
		r.Get("/*", rp.RepoTree)
	})
	r.Get("/commit/{ref}", rp.RepoCommit)
	r.Get("/branches", rp.RepoBranches)
	r.Route("/tags", func(r chi.Router) {
		r.Get("/", rp.RepoTags)
		r.Route("/{tag}", func(r chi.Router) {
			r.Use(middleware.AuthMiddleware(rp.oauth))
			// require auth to download for now
			r.Get("/download/{file}", rp.DownloadArtifact)

			// require repo:push to upload or delete artifacts
			//
			// additionally: only the uploader can truly delete an artifact
			// (record+blob will live on their pds)
			r.Group(func(r chi.Router) {
				r.With(mw.RepoPermissionMiddleware("repo:push"))
				r.Post("/upload", rp.AttachArtifact)
				r.Delete("/{file}", rp.DeleteArtifact)
			})
		})
	})
	r.Get("/blob/{ref}/*", rp.RepoBlob)
	r.Get("/raw/{ref}/*", rp.RepoBlobRaw)

	r.Route("/fork", func(r chi.Router) {
		r.Use(middleware.AuthMiddleware(rp.oauth))
		r.Get("/", rp.ForkRepo)
		r.Post("/", rp.ForkRepo)
		r.With(mw.RepoPermissionMiddleware("repo:owner")).Route("/sync", func(r chi.Router) {
			r.Post("/", rp.SyncRepoFork)
		})
	})

	r.Route("/compare", func(r chi.Router) {
		r.Get("/", rp.RepoCompareNew) // start an new comparison

		// we have to wildcard here since we want to support GitHub's compare syntax
		//   /compare/{ref1}...{ref2}
		// for example:
		//   /compare/master...some/feature
		//   /compare/master...example.com:another/feature <- this is a fork
		r.Get("/{base}/{head}", rp.RepoCompare)
		r.Get("/*", rp.RepoCompare)
	})

	// settings routes, needs auth
	r.Group(func(r chi.Router) {
		r.Use(middleware.AuthMiddleware(rp.oauth))
		// repo description can only be edited by owner
		r.With(mw.RepoPermissionMiddleware("repo:owner")).Route("/description", func(r chi.Router) {
			r.Put("/", rp.RepoDescription)
			r.Get("/", rp.RepoDescription)
			r.Get("/edit", rp.RepoDescriptionEdit)
		})
		r.With(mw.RepoPermissionMiddleware("repo:settings")).Route("/settings", func(r chi.Router) {
			r.Get("/", rp.RepoSettings)
			r.With(mw.RepoPermissionMiddleware("repo:invite")).Put("/collaborator", rp.AddCollaborator)
			r.With(mw.RepoPermissionMiddleware("repo:delete")).Delete("/delete", rp.DeleteRepo)
			r.Put("/branches/default", rp.SetDefaultBranch)
		})
	})

	return r
}
