package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/icyphox/bild/legit/config"
)

// Checks for gitprotocol-http(5) specific smells; if found, passes
// the request on to the git http service, else render the web frontend.
func (d *deps) Multiplex(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")

	if r.URL.RawQuery == "service=git-receive-pack" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("no pushing allowed!"))
		return
	}

	if path == "info/refs" &&
		r.URL.RawQuery == "service=git-upload-pack" &&
		r.Method == "GET" {
		d.InfoRefs(w, r)
	} else if path == "git-upload-pack" && r.Method == "POST" {
		d.UploadPack(w, r)
	} else if r.Method == "GET" {
		d.RepoIndex(w, r)
	}
}

func Handlers(c *config.Config) http.Handler {
	r := chi.NewRouter()
	d := deps{c}

	r.Get("/static/{file}", d.ServeStatic)

	r.Route("/@{user}", func(r chi.Router) {
		r.Get("/", d.Index)
		r.Route("/{name}", func(r chi.Router) {
			r.Get("/", d.Multiplex)
			r.Post("/", d.Multiplex)

			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/*", d.RepoTree)
			})

			r.Route("/blob/{ref}", func(r chi.Router) {
				r.Get("/*", d.FileContent)
			})

			r.Get("/log/{ref}", d.Log)
			r.Get("/archive/{file}", d.Archive)
			r.Get("/commit/{ref}", d.Diff)
			r.Get("/refs/", d.Refs)

			// Catch-all routes
			r.Get("/*", d.Multiplex)
			r.Post("/*", d.Multiplex)
		})
	})

	return r
}
