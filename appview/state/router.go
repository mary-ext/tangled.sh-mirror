package state

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/state/settings"
	"tangled.sh/tangled.sh/core/appview/state/userutil"
)

func (s *State) Router() http.Handler {
	router := chi.NewRouter()

	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		pat := chi.URLParam(r, "*")
		if strings.HasPrefix(pat, "did:") || strings.HasPrefix(pat, "@") {
			s.UserRouter().ServeHTTP(w, r)
		} else {
			// Check if the first path element is a valid handle without '@' or a flattened DID
			pathParts := strings.SplitN(pat, "/", 2)
			if len(pathParts) > 0 {
				if userutil.IsHandleNoAt(pathParts[0]) {
					// Redirect to the same path but with '@' prefixed to the handle
					redirectPath := "@" + pat
					http.Redirect(w, r, "/"+redirectPath, http.StatusFound)
					return
				} else if userutil.IsFlattenedDid(pathParts[0]) {
					// Redirect to the unflattened DID version
					unflattenedDid := userutil.UnflattenDid(pathParts[0])
					var redirectPath string
					if len(pathParts) > 1 {
						redirectPath = unflattenedDid + "/" + pathParts[1]
					} else {
						redirectPath = unflattenedDid
					}
					http.Redirect(w, r, "/"+redirectPath, http.StatusFound)
					return
				}
			}
			s.StandardRouter().ServeHTTP(w, r)
		}
	})

	return router
}

func (s *State) UserRouter() http.Handler {
	r := chi.NewRouter()

	// strip @ from user
	r.Use(StripLeadingAt)

	r.With(ResolveIdent(s)).Route("/{user}", func(r chi.Router) {
		r.Get("/", s.ProfilePage)
		r.With(ResolveRepo(s)).Route("/{repo}", func(r chi.Router) {
			r.Get("/", s.RepoIndex)
			r.Get("/commits/{ref}", s.RepoLog)
			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/", s.RepoIndex)
				r.Get("/*", s.RepoTree)
			})
			r.Get("/commit/{ref}", s.RepoCommit)
			r.Get("/branches", s.RepoBranches)
			r.Get("/tags", s.RepoTags)
			r.Get("/blob/{ref}/*", s.RepoBlob)
			r.Get("/blob/{ref}/raw/*", s.RepoBlobRaw)

			r.Route("/issues", func(r chi.Router) {
				r.Get("/", s.RepoIssues)
				r.Get("/{issue}", s.RepoSingleIssue)

				r.Group(func(r chi.Router) {
					r.Use(middleware.AuthMiddleware(s.auth))
					r.Get("/new", s.NewIssue)
					r.Post("/new", s.NewIssue)
					r.Post("/{issue}/comment", s.NewIssueComment)
					r.Route("/{issue}/comment/{comment_id}/", func(r chi.Router) {
						r.Get("/", s.IssueComment)
						r.Delete("/", s.DeleteIssueComment)
						r.Get("/edit", s.EditIssueComment)
						r.Post("/edit", s.EditIssueComment)
					})
					r.Post("/{issue}/close", s.CloseIssue)
					r.Post("/{issue}/reopen", s.ReopenIssue)
				})
			})

			r.Route("/fork", func(r chi.Router) {
				r.Use(middleware.AuthMiddleware(s.auth))
				r.Get("/", s.ForkRepo)
				r.Post("/", s.ForkRepo)
			})

			r.Route("/pulls", func(r chi.Router) {
				r.Get("/", s.RepoPulls)
				r.With(middleware.AuthMiddleware(s.auth)).Route("/new", func(r chi.Router) {
					r.Get("/", s.NewPull)
					r.Get("/patch-upload", s.PatchUploadFragment)
					r.Post("/validate-patch", s.ValidatePatch)
					r.Get("/compare-branches", s.CompareBranchesFragment)
					r.Get("/compare-forks", s.CompareForksFragment)
					r.Get("/fork-branches", s.CompareForksBranchesFragment)
					r.Post("/", s.NewPull)
				})

				r.Route("/{pull}", func(r chi.Router) {
					r.Use(ResolvePull(s))
					r.Get("/", s.RepoSinglePull)

					r.Route("/round/{round}", func(r chi.Router) {
						r.Get("/", s.RepoPullPatch)
						r.Get("/interdiff", s.RepoPullInterdiff)
						r.Get("/actions", s.PullActions)
						r.With(middleware.AuthMiddleware(s.auth)).Route("/comment", func(r chi.Router) {
							r.Get("/", s.PullComment)
							r.Post("/", s.PullComment)
						})
					})

					r.Route("/round/{round}.patch", func(r chi.Router) {
						r.Get("/", s.RepoPullPatchRaw)
					})

					r.Group(func(r chi.Router) {
						r.Use(middleware.AuthMiddleware(s.auth))
						r.Route("/resubmit", func(r chi.Router) {
							r.Get("/", s.ResubmitPull)
							r.Post("/", s.ResubmitPull)
						})
						r.Post("/close", s.ClosePull)
						r.Post("/reopen", s.ReopenPull)
						// collaborators only
						r.Group(func(r chi.Router) {
							r.Use(RepoPermissionMiddleware(s, "repo:push"))
							r.Post("/merge", s.MergePull)
							// maybe lock, etc.
						})
					})
				})
			})

			// These routes get proxied to the knot
			r.Get("/info/refs", s.InfoRefs)
			r.Post("/git-upload-pack", s.UploadPack)

			// settings routes, needs auth
			r.Group(func(r chi.Router) {
				r.Use(middleware.AuthMiddleware(s.auth))
				// repo description can only be edited by owner
				r.With(RepoPermissionMiddleware(s, "repo:owner")).Route("/description", func(r chi.Router) {
					r.Put("/", s.RepoDescription)
					r.Get("/", s.RepoDescription)
					r.Get("/edit", s.RepoDescriptionEdit)
				})
				r.With(RepoPermissionMiddleware(s, "repo:settings")).Route("/settings", func(r chi.Router) {
					r.Get("/", s.RepoSettings)
					r.With(RepoPermissionMiddleware(s, "repo:invite")).Put("/collaborator", s.AddCollaborator)
					r.With(RepoPermissionMiddleware(s, "repo:delete")).Delete("/delete", s.DeleteRepo)
					r.Put("/branches/default", s.SetDefaultBranch)
				})
			})
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.pages.Error404(w)
	})

	return r
}

func (s *State) StandardRouter() http.Handler {
	r := chi.NewRouter()

	r.Handle("/static/*", s.pages.Static())

	r.Get("/", s.Timeline)

	r.With(middleware.AuthMiddleware(s.auth)).Post("/logout", s.Logout)

	r.Route("/login", func(r chi.Router) {
		r.Get("/", s.Login)
		r.Post("/", s.Login)
	})

	r.Route("/knots", func(r chi.Router) {
		r.Use(middleware.AuthMiddleware(s.auth))
		r.Get("/", s.Knots)
		r.Post("/key", s.RegistrationKey)

		r.Route("/{domain}", func(r chi.Router) {
			r.Post("/init", s.InitKnotServer)
			r.Get("/", s.KnotServerInfo)
			r.Route("/member", func(r chi.Router) {
				r.Use(KnotOwner(s))
				r.Get("/", s.ListMembers)
				r.Put("/", s.AddMember)
				r.Delete("/", s.RemoveMember)
			})
		})
	})

	r.Route("/repo", func(r chi.Router) {
		r.Route("/new", func(r chi.Router) {
			r.Use(middleware.AuthMiddleware(s.auth))
			r.Get("/", s.NewRepo)
			r.Post("/", s.NewRepo)
		})
		// r.Post("/import", s.ImportRepo)
	})

	r.With(middleware.AuthMiddleware(s.auth)).Route("/follow", func(r chi.Router) {
		r.Post("/", s.Follow)
		r.Delete("/", s.Follow)
	})

	r.With(middleware.AuthMiddleware(s.auth)).Route("/star", func(r chi.Router) {
		r.Post("/", s.Star)
		r.Delete("/", s.Star)
	})

	r.Route("/settings", s.SettingsRouter)

	r.Get("/keys/{user}", s.Keys)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.pages.Error404(w)
	})
	return r
}

func (s *State) SettingsRouter(r chi.Router) {
	settings := &settings.Settings{
		Db:     s.db,
		Auth:   s.auth,
		Pages:  s.pages,
		Config: s.config,
	}

	settings.Router(r)
}
