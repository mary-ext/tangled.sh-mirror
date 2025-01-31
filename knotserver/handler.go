package knotserver

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/icyphox/bild/knotserver/config"
	"github.com/icyphox/bild/knotserver/db"
)

func Setup(c *config.Config, db *db.DB) (http.Handler, error) {
	r := chi.NewRouter()

	h := Handle{
		c:  c,
		db: db,
	}

	r.Route("/settings", func(r chi.Router) {
		r.Get("/keys", h.Keys)
		r.Put("/keys", h.Keys)
	})

	r.Get("/", h.Index)
	r.Route("/{did}", func(r chi.Router) {
		// Repo routes
		r.Route("/{name}", func(r chi.Router) {
			r.Get("/", h.RepoIndex)
			r.Get("/info/refs", h.InfoRefs)
			r.Post("/git-upload-pack", h.UploadPack)

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
		})
	})

	r.Route("/repo", func(r chi.Router) {
		r.Put("/new", h.NewRepo)
	})

	r.Route("/internal", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Get("/health", h.Health)
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

	fmt.Println(r.URL.RawQuery)
	fmt.Println(r.Method)

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
