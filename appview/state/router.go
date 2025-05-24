package state

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
	"tangled.sh/tangled.sh/core/appview/middleware"
	oauthhandler "tangled.sh/tangled.sh/core/appview/oauth/handler"
	"tangled.sh/tangled.sh/core/appview/pulls"
	"tangled.sh/tangled.sh/core/appview/settings"
	"tangled.sh/tangled.sh/core/appview/state/userutil"
)

func (s *State) Router() http.Handler {
	router := chi.NewRouter()
	middleware := middleware.New(
		s.oauth,
		s.db,
		s.enforcer,
		s.repoResolver,
		s.resolver,
		s.pages,
	)

	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		pat := chi.URLParam(r, "*")
		if strings.HasPrefix(pat, "did:") || strings.HasPrefix(pat, "@") {
			s.UserRouter(&middleware).ServeHTTP(w, r)
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
			s.StandardRouter(&middleware).ServeHTTP(w, r)
		}
	})

	return router
}

func (s *State) UserRouter(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	// strip @ from user
	r.Use(middleware.StripLeadingAt)

	r.With(mw.ResolveIdent()).Route("/{user}", func(r chi.Router) {
		r.Get("/", s.Profile)

		r.With(mw.ResolveRepo()).Route("/{repo}", func(r chi.Router) {
			r.Use(mw.GoImport())

			r.Get("/", s.RepoIndex)
			r.Get("/commits/{ref}", s.RepoLog)
			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/", s.RepoIndex)
				r.Get("/*", s.RepoTree)
			})
			r.Get("/commit/{ref}", s.RepoCommit)
			r.Get("/branches", s.RepoBranches)
			r.Route("/tags", func(r chi.Router) {
				r.Get("/", s.RepoTags)
				r.Route("/{tag}", func(r chi.Router) {
					r.Use(middleware.AuthMiddleware(s.oauth))
					// require auth to download for now
					r.Get("/download/{file}", s.DownloadArtifact)

					// require repo:push to upload or delete artifacts
					//
					// additionally: only the uploader can truly delete an artifact
					// (record+blob will live on their pds)
					r.Group(func(r chi.Router) {
						r.With(mw.RepoPermissionMiddleware("repo:push"))
						r.Post("/upload", s.AttachArtifact)
						r.Delete("/{file}", s.DeleteArtifact)
					})
				})
			})
			r.Get("/blob/{ref}/*", s.RepoBlob)
			r.Get("/raw/{ref}/*", s.RepoBlobRaw)

			r.Route("/issues", func(r chi.Router) {
				r.With(middleware.Paginate).Get("/", s.RepoIssues)
				r.Get("/{issue}", s.RepoSingleIssue)

				r.Group(func(r chi.Router) {
					r.Use(middleware.AuthMiddleware(s.oauth))
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
				r.Use(middleware.AuthMiddleware(s.oauth))
				r.Get("/", s.ForkRepo)
				r.Post("/", s.ForkRepo)
				r.With(mw.RepoPermissionMiddleware("repo:owner")).Route("/sync", func(r chi.Router) {
					r.Post("/", s.SyncRepoFork)
				})
			})

			r.Route("/compare", func(r chi.Router) {
				r.Get("/", s.RepoCompareNew) // start an new comparison

				// we have to wildcard here since we want to support GitHub's compare syntax
				//   /compare/{ref1}...{ref2}
				// for example:
				//   /compare/master...some/feature
				//   /compare/master...example.com:another/feature <- this is a fork
				r.Get("/{base}/{head}", s.RepoCompare)
				r.Get("/*", s.RepoCompare)
			})

			r.Mount("/pulls", s.PullsRouter(mw))

			// These routes get proxied to the knot
			r.Get("/info/refs", s.InfoRefs)
			r.Post("/git-upload-pack", s.UploadPack)
			r.Post("/git-receive-pack", s.ReceivePack)

			// settings routes, needs auth
			r.Group(func(r chi.Router) {
				r.Use(middleware.AuthMiddleware(s.oauth))
				// repo description can only be edited by owner
				r.With(mw.RepoPermissionMiddleware("repo:owner")).Route("/description", func(r chi.Router) {
					r.Put("/", s.RepoDescription)
					r.Get("/", s.RepoDescription)
					r.Get("/edit", s.RepoDescriptionEdit)
				})
				r.With(mw.RepoPermissionMiddleware("repo:settings")).Route("/settings", func(r chi.Router) {
					r.Get("/", s.RepoSettings)
					r.With(mw.RepoPermissionMiddleware("repo:invite")).Put("/collaborator", s.AddCollaborator)
					r.With(mw.RepoPermissionMiddleware("repo:delete")).Delete("/delete", s.DeleteRepo)
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

func (s *State) StandardRouter(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.Handle("/static/*", s.pages.Static())

	r.Get("/", s.Timeline)

	r.With(middleware.AuthMiddleware(s.oauth)).Post("/logout", s.Logout)

	r.Route("/knots", func(r chi.Router) {
		r.Use(middleware.AuthMiddleware(s.oauth))
		r.Get("/", s.Knots)
		r.Post("/key", s.RegistrationKey)

		r.Route("/{domain}", func(r chi.Router) {
			r.Post("/init", s.InitKnotServer)
			r.Get("/", s.KnotServerInfo)
			r.Route("/member", func(r chi.Router) {
				r.Use(mw.KnotOwner())
				r.Get("/", s.ListMembers)
				r.Put("/", s.AddMember)
				r.Delete("/", s.RemoveMember)
			})
		})
	})

	r.Route("/repo", func(r chi.Router) {
		r.Route("/new", func(r chi.Router) {
			r.Use(middleware.AuthMiddleware(s.oauth))
			r.Get("/", s.NewRepo)
			r.Post("/", s.NewRepo)
		})
		// r.Post("/import", s.ImportRepo)
	})

	r.With(middleware.AuthMiddleware(s.oauth)).Route("/follow", func(r chi.Router) {
		r.Post("/", s.Follow)
		r.Delete("/", s.Follow)
	})

	r.With(middleware.AuthMiddleware(s.oauth)).Route("/star", func(r chi.Router) {
		r.Post("/", s.Star)
		r.Delete("/", s.Star)
	})

	r.Route("/profile", func(r chi.Router) {
		r.Use(middleware.AuthMiddleware(s.oauth))
		r.Get("/edit-bio", s.EditBioFragment)
		r.Get("/edit-pins", s.EditPinsFragment)
		r.Post("/bio", s.UpdateProfileBio)
		r.Post("/pins", s.UpdateProfilePins)
	})

	r.Mount("/settings", s.SettingsRouter())
	r.Mount("/", s.OAuthRouter())

	r.Get("/keys/{user}", s.Keys)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.pages.Error404(w)
	})
	return r
}

func (s *State) OAuthRouter() http.Handler {
	oauth := &oauthhandler.OAuthHandler{
		Config:   s.config,
		Pages:    s.pages,
		Resolver: s.resolver,
		Db:       s.db,
		Store:    sessions.NewCookieStore([]byte(s.config.Core.CookieSecret)),
		OAuth:    s.oauth,
		Enforcer: s.enforcer,
		Posthog:  s.posthog,
	}

	return oauth.Router()
}

func (s *State) SettingsRouter() http.Handler {
	settings := &settings.Settings{
		Db:     s.db,
		OAuth:  s.oauth,
		Pages:  s.pages,
		Config: s.config,
	}

	return settings.Router()
}

func (s *State) PullsRouter(mw *middleware.Middleware) http.Handler {
	pulls := pulls.New(s.oauth, s.repoResolver, s.pages, s.resolver, s.db, s.config)
	return pulls.Router(mw)
}
