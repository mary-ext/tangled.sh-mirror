package pipelines

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/middleware"
)

func (p *Pipelines) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()
	r.Get("/", p.Index)
	r.Get("/{pipeline}/workflow/{workflow}", p.Workflow)
	r.Get("/{pipeline}/workflow/{workflow}/logs", p.Logs)

	return r
}
