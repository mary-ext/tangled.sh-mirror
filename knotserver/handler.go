package knotserver

import (
	"net/http"

	"github.com/go-chi/chi"
	"github.com/icyphox/bild/config"
	"github.com/icyphox/bild/db"
)

func Setup(c *config.Config, db *db.DB) (http.Handler, error) {
	r := chi.NewRouter()

	h := Handle{
		c:  c,
		db: db,
	}

	// r.Route("/repo", func(r chi.Router) {
	// 	r.Use(h.AuthMiddleware)
	// 	r.Get("/new", h.NewRepo)
	// 	r.Put("/new", h.NewRepo)
	// })

	r.Route("/{did}", func(r chi.Router) {
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

type Handle struct {
	c  *config.Config
	db *db.DB
}

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
