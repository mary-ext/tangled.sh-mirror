package state

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/issues"
	"tangled.org/core/appview/knots"
	"tangled.org/core/appview/labels"
	"tangled.org/core/appview/middleware"
	"tangled.org/core/appview/notifications"
	"tangled.org/core/appview/pipelines"
	"tangled.org/core/appview/pulls"
	"tangled.org/core/appview/repo"
	"tangled.org/core/appview/settings"
	"tangled.org/core/appview/signup"
	"tangled.org/core/appview/spindles"
	"tangled.org/core/appview/state/userutil"
	avstrings "tangled.org/core/appview/strings"
	"tangled.org/core/log"
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

	router.Get("/favicon.svg", s.Favicon)
	router.Get("/favicon.ico", s.Favicon)
	router.Get("/pwa-manifest.json", s.PWAManifest)
	router.Get("/robots.txt", s.RobotsTxt)

	userRouter := s.UserRouter(&middleware)
	standardRouter := s.StandardRouter(&middleware)

	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		pat := chi.URLParam(r, "*")
		if strings.HasPrefix(pat, "did:") || strings.HasPrefix(pat, "@") {
			userRouter.ServeHTTP(w, r)
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
			standardRouter.ServeHTTP(w, r)
		}
	})

	return router
}

func (s *State) UserRouter(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.With(mw.ResolveIdent()).Route("/{user}", func(r chi.Router) {
		r.Get("/", s.Profile)
		r.Get("/feed.atom", s.AtomFeedPage)

		// redirect /@handle/repo.git -> /@handle/repo
		r.Get("/{repo}.git", func(w http.ResponseWriter, r *http.Request) {
			nonDotGitPath := strings.TrimSuffix(r.URL.Path, ".git")
			http.Redirect(w, r, nonDotGitPath, http.StatusMovedPermanently)
		})

		r.With(mw.ResolveRepo()).Route("/{repo}", func(r chi.Router) {
			r.Use(mw.GoImport())
			r.Mount("/", s.RepoRouter(mw))
			r.Mount("/issues", s.IssuesRouter(mw))
			r.Mount("/pulls", s.PullsRouter(mw))
			r.Mount("/pipelines", s.PipelinesRouter(mw))
			r.Mount("/labels", s.LabelsRouter(mw))

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

	r.Get("/", s.HomeOrTimeline)
	r.Get("/timeline", s.Timeline)
	r.Get("/upgradeBanner", s.UpgradeBanner)

	// special-case handler for serving tangled.org/core
	r.Get("/core", s.Core())

	r.Get("/login", s.Login)
	r.Post("/login", s.Login)
	r.Post("/logout", s.Logout)

	r.Route("/repo", func(r chi.Router) {
		r.Route("/new", func(r chi.Router) {
			r.Use(middleware.AuthMiddleware(s.oauth))
			r.Get("/", s.NewRepo)
			r.Post("/", s.NewRepo)
		})
		// r.Post("/import", s.ImportRepo)
	})

	r.Get("/goodfirstissues", s.GoodFirstIssues)

	r.With(middleware.AuthMiddleware(s.oauth)).Route("/follow", func(r chi.Router) {
		r.Post("/", s.Follow)
		r.Delete("/", s.Follow)
	})

	r.With(middleware.AuthMiddleware(s.oauth)).Route("/star", func(r chi.Router) {
		r.Post("/", s.Star)
		r.Delete("/", s.Star)
	})

	r.With(middleware.AuthMiddleware(s.oauth)).Route("/react", func(r chi.Router) {
		r.Post("/", s.React)
		r.Delete("/", s.React)
	})

	r.Route("/profile", func(r chi.Router) {
		r.Use(middleware.AuthMiddleware(s.oauth))
		r.Get("/edit-bio", s.EditBioFragment)
		r.Get("/edit-pins", s.EditPinsFragment)
		r.Post("/bio", s.UpdateProfileBio)
		r.Post("/pins", s.UpdateProfilePins)
	})

	r.Mount("/settings", s.SettingsRouter())
	r.Mount("/strings", s.StringsRouter(mw))
	r.Mount("/knots", s.KnotsRouter())
	r.Mount("/spindles", s.SpindlesRouter())
	r.Mount("/notifications", s.NotificationsRouter(mw))

	r.Mount("/signup", s.SignupRouter())
	r.Mount("/", s.oauth.Router())

	r.Get("/keys/{user}", s.Keys)
	r.Get("/terms", s.TermsOfService)
	r.Get("/privacy", s.PrivacyPolicy)
	r.Get("/brand", s.Brand)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.pages.Error404(w)
	})
	return r
}

// Core serves tangled.org/core go-import meta tags, and redirects
// to the core repository if accessed normally.
func (s *State) Core() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("go-get") == "1" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<meta name="go-import" content="tangled.org/core git https://tangled.org/@tangled.org/core">`))
			return
		}

		http.Redirect(w, r, "/@tangled.org/core", http.StatusFound)
	}
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

func (s *State) SpindlesRouter() http.Handler {
	logger := log.New("spindles")

	spindles := &spindles.Spindles{
		Db:         s.db,
		OAuth:      s.oauth,
		Pages:      s.pages,
		Config:     s.config,
		Enforcer:   s.enforcer,
		IdResolver: s.idResolver,
		Logger:     logger,
	}

	return spindles.Router()
}

func (s *State) KnotsRouter() http.Handler {
	logger := log.New("knots")

	knots := &knots.Knots{
		Db:         s.db,
		OAuth:      s.oauth,
		Pages:      s.pages,
		Config:     s.config,
		Enforcer:   s.enforcer,
		IdResolver: s.idResolver,
		Knotstream: s.knotstream,
		Logger:     logger,
	}

	return knots.Router()
}

func (s *State) StringsRouter(mw *middleware.Middleware) http.Handler {
	logger := log.New("strings")

	strs := &avstrings.Strings{
		Db:         s.db,
		OAuth:      s.oauth,
		Pages:      s.pages,
		IdResolver: s.idResolver,
		Notifier:   s.notifier,
		Logger:     logger,
	}

	return strs.Router(mw)
}

func (s *State) IssuesRouter(mw *middleware.Middleware) http.Handler {
	issues := issues.New(s.oauth, s.repoResolver, s.pages, s.idResolver, s.db, s.config, s.notifier, s.validator)
	return issues.Router(mw)
}

func (s *State) PullsRouter(mw *middleware.Middleware) http.Handler {
	pulls := pulls.New(s.oauth, s.repoResolver, s.pages, s.idResolver, s.db, s.config, s.notifier)
	return pulls.Router(mw)
}

func (s *State) RepoRouter(mw *middleware.Middleware) http.Handler {
	logger := log.New("repo")
	repo := repo.New(s.oauth, s.repoResolver, s.pages, s.spindlestream, s.idResolver, s.db, s.config, s.notifier, s.enforcer, logger, s.validator)
	return repo.Router(mw)
}

func (s *State) PipelinesRouter(mw *middleware.Middleware) http.Handler {
	pipes := pipelines.New(s.oauth, s.repoResolver, s.pages, s.spindlestream, s.idResolver, s.db, s.config, s.enforcer)
	return pipes.Router(mw)
}

func (s *State) LabelsRouter(mw *middleware.Middleware) http.Handler {
	ls := labels.New(s.oauth, s.pages, s.db, s.validator, s.enforcer)
	return ls.Router(mw)
}

func (s *State) NotificationsRouter(mw *middleware.Middleware) http.Handler {
	notifs := notifications.New(s.db, s.oauth, s.pages)
	return notifs.Router(mw)
}

func (s *State) SignupRouter() http.Handler {
	logger := log.New("signup")

	sig := signup.New(s.config, s.db, s.posthog, s.idResolver, s.pages, logger)
	return sig.Router()
}
