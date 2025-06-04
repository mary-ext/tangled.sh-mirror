package state

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
	"tangled.sh/tangled.sh/core/appview/issues"
	"tangled.sh/tangled.sh/core/appview/middleware"
	oauthhandler "tangled.sh/tangled.sh/core/appview/oauth/handler"
	"tangled.sh/tangled.sh/core/appview/pulls"
	"tangled.sh/tangled.sh/core/appview/repo"
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
		s.idResolver,
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

			r.Mount("/", s.RepoRouter(mw))
			r.Mount("/issues", s.IssuesRouter(mw))
			r.Mount("/pulls", s.PullsRouter(mw))

			// These routes get proxied to the knot
			r.Get("/info/refs", s.InfoRefs)
			r.Post("/git-upload-pack", s.UploadPack)
			r.Post("/git-receive-pack", s.ReceivePack)

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
	store := sessions.NewCookieStore([]byte(s.config.Core.CookieSecret))
	oauth := oauthhandler.New(s.config, s.pages, s.idResolver, s.db, s.sess, store, s.oauth, s.enforcer, s.posthog)
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

func (s *State) IssuesRouter(mw *middleware.Middleware) http.Handler {
	issues := issues.New(s.oauth, s.repoResolver, s.pages, s.idResolver, s.db, s.config, s.posthog)
	return issues.Router(mw)

}

func (s *State) PullsRouter(mw *middleware.Middleware) http.Handler {
	pulls := pulls.New(s.oauth, s.repoResolver, s.pages, s.idResolver, s.db, s.config, s.posthog)
	return pulls.Router(mw)
}

func (s *State) RepoRouter(mw *middleware.Middleware) http.Handler {
	repo := repo.New(s.oauth, s.repoResolver, s.pages, s.idResolver, s.db, s.config, s.posthog, s.enforcer)
	return repo.Router(mw)
}
