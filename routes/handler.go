package routes

import (
	"fmt"
	"net/http"

	_ "github.com/bluesky-social/indigo/atproto/identity"
	_ "github.com/bluesky-social/indigo/atproto/syntax"
	_ "github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
	"github.com/icyphox/bild/auth"
	"github.com/icyphox/bild/config"
	database "github.com/icyphox/bild/db"
	"github.com/icyphox/bild/routes/middleware"
	"github.com/icyphox/bild/routes/tmpl"
)

// Checks for gitprotocol-http(5) specific smells; if found, passes
// the request on to the git http service, else render the web frontend.
func (h *Handle) Multiplex(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")

	if r.URL.RawQuery == "service=git-receive-pack" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("no pushing allowed!"))
		return
	}

	if path == "info/refs" &&
		r.URL.RawQuery == "service=git-upload-pack" &&
		r.Method == "GET" {
		h.InfoRefs(w, r)
	} else if path == "git-upload-pack" && r.Method == "POST" {
		h.UploadPack(w, r)
	} else if r.Method == "GET" {
		h.RepoIndex(w, r)
	}
}

func Setup(c *config.Config, db *database.DB) (http.Handler, error) {
	r := chi.NewRouter()
	s := sessions.NewCookieStore([]byte("TODO_CHANGE_ME"))
	t, err := tmpl.Load(c.Dirs.Templates)
	if err != nil {
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}

	auth := auth.NewAuth(s)

	h := Handle{
		c:    c,
		t:    t,
		s:    s,
		db:   db,
		auth: auth,
	}

	r.Get("/", h.Timeline)

	r.Group(func(r chi.Router) {
		r.Get("/login", h.Login)
		r.Post("/login", h.Login)
	})
	r.Get("/static/{file}", h.ServeStatic)

	r.Route("/repo", func(r chi.Router) {
		r.Use(h.AuthMiddleware)
		r.Get("/new", h.NewRepo)
		r.Put("/new", h.NewRepo)
	})

	r.Group(func(r chi.Router) {
		r.Use(h.AuthMiddleware)
		r.Route("/settings", func(r chi.Router) {
			r.Get("/keys", h.Keys)
			r.Put("/keys", h.Keys)
		})
	})

	r.Route("/@{user}", func(r chi.Router) {
		r.Use(middleware.AddDID)
		r.Get("/", h.Index)

		// Repo routes
		r.Route("/{name}", func(r chi.Router) {
			r.Get("/", h.Multiplex)
			r.Post("/", h.Multiplex)

			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/*", h.RepoTree)
			})

			r.Route("/blob/{ref}", func(r chi.Router) {
				r.Get("/*", h.FileContent)
			})

			r.Get("/log/{ref}", h.Log)
			r.Get("/archive/{file}", h.Archive)
			r.Get("/commit/{ref}", h.Diff)
			r.Get("/refs/", h.Refs)

			r.Group(func(r chi.Router) {
				// settings page is only accessible to owners
				r.Use(h.AccessLevel(database.Owner))
				r.Route("/settings", func(r chi.Router) {
					r.Put("/collaborators", h.Collaborators)
				})
			})

			// Catch-all routes
			r.Get("/*", h.Multiplex)
			r.Post("/*", h.Multiplex)
		})
	})

	return r, nil
}
