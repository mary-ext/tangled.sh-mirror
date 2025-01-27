package routes

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/icyphox/bild/legit/config"
	"github.com/icyphox/bild/legit/db"
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

func Setup(c *config.Config) (http.Handler, error) {
	r := chi.NewRouter()
	t := template.Must(template.ParseGlob(filepath.Join(c.Dirs.Templates, "*")))
	db, err := db.Setup(c.Server.DBPath)

	if err != nil {
		return nil, fmt.Errorf("failed to setup db: %w", err)
	}

	h := Handle{
		c:  c,
		t:  t,
		db: db,
	}

	r.Get("/login", h.Login)
	r.Get("/static/{file}", h.ServeStatic)

	r.Route("/repo", func(r chi.Router) {
		r.Get("/new", h.NewRepo)
		r.Put("/new", h.NewRepo)
	})

	r.Route("/settings", func(r chi.Router) {
		r.Get("/keys", h.Keys)
		r.Put("/keys", h.Keys)
	})

	r.Route("/@{user}", func(r chi.Router) {
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

			// Catch-all routes
			r.Get("/*", h.Multiplex)
			r.Post("/*", h.Multiplex)
		})
	})

	return r, nil
}
