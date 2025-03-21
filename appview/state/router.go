package state

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *State) Router() http.Handler {
	router := chi.NewRouter()

	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		pat := chi.URLParam(r, "*")
		if strings.HasPrefix(pat, "did:") || strings.HasPrefix(pat, "@") {
			s.UserRouter().ServeHTTP(w, r)
		} else {
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

			r.Route("/issues", func(r chi.Router) {
				r.Get("/", s.RepoIssues)
				r.Get("/{issue}", s.RepoSingleIssue)

				r.Group(func(r chi.Router) {
					r.Use(AuthMiddleware(s))
					r.Get("/new", s.NewIssue)
					r.Post("/new", s.NewIssue)
					r.Post("/{issue}/comment", s.IssueComment)
					r.Post("/{issue}/close", s.CloseIssue)
					r.Post("/{issue}/reopen", s.ReopenIssue)
				})
			})

			r.Route("/pulls", func(r chi.Router) {
				r.Get("/", s.RepoPulls)
				r.With(AuthMiddleware(s)).Route("/new", func(r chi.Router) {
					r.Get("/", s.NewPull)
					r.Post("/", s.NewPull)
				})

				r.Route("/{pull}", func(r chi.Router) {
					r.Use(ResolvePull(s))
					r.Get("/", s.RepoSinglePull)

					r.Route("/round/{round}", func(r chi.Router) {
						r.Get("/", s.RepoPullPatch)
						r.Get("/actions", s.PullActions)
						r.Route("/comment", func(r chi.Router) {
							r.Get("/", s.PullComment)
							r.Post("/", s.PullComment)
						})
					})

					// authorized requests below this point
					r.Group(func(r chi.Router) {
						r.Use(AuthMiddleware(s))
						r.Route("/resubmit", func(r chi.Router) {
							r.Get("/", s.ResubmitPull)
							r.Post("/", s.ResubmitPull)
						})
						r.Route("/comment", func(r chi.Router) {
							r.Get("/", s.PullComment)
							r.Post("/", s.PullComment)
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
				r.Use(AuthMiddleware(s))
				// repo description can only be edited by owner
				r.With(RepoPermissionMiddleware(s, "repo:owner")).Route("/description", func(r chi.Router) {
					r.Put("/", s.RepoDescription)
					r.Get("/", s.RepoDescription)
					r.Get("/edit", s.RepoDescriptionEdit)
				})
				r.With(RepoPermissionMiddleware(s, "repo:settings")).Route("/settings", func(r chi.Router) {
					r.Get("/", s.RepoSettings)
					r.With(RepoPermissionMiddleware(s, "repo:invite")).Put("/collaborator", s.AddCollaborator)
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

	r.With(AuthMiddleware(s)).Get("/logout", s.Logout)

	r.Route("/login", func(r chi.Router) {
		r.Get("/", s.Login)
		r.Post("/", s.Login)
	})

	r.Route("/knots", func(r chi.Router) {
		r.Use(AuthMiddleware(s))
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
			r.Use(AuthMiddleware(s))
			r.Get("/", s.NewRepo)
			r.Post("/", s.NewRepo)
		})
		// r.Post("/import", s.ImportRepo)
	})

	r.With(AuthMiddleware(s)).Route("/follow", func(r chi.Router) {
		r.Post("/", s.Follow)
		r.Delete("/", s.Follow)
	})

	r.With(AuthMiddleware(s)).Route("/star", func(r chi.Router) {
		r.Post("/", s.Star)
		r.Delete("/", s.Star)
	})

	r.Route("/settings", func(r chi.Router) {
		r.Use(AuthMiddleware(s))
		r.Get("/", s.Settings)
		r.Put("/keys", s.SettingsKeys)
		r.Put("/emails", s.SettingsEmails)
		r.Delete("/emails", s.SettingsEmails)
		r.Get("/emails/verify", s.SettingsEmailsVerify)
		r.Post("/emails/verify/resend", s.SettingsEmailsVerifyResend)
		r.Post("/emails/primary", s.SettingsEmailsPrimary)
	})

	r.Get("/keys/{user}", s.Keys)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.pages.Error404(w)
	})
	return r
}
