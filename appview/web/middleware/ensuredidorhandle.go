package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/state/userutil"
)

// EnsureDidOrHandle ensures the "user" url param is valid did/handle format.
// If not, respond with 404
func EnsureDidOrHandle(p *pages.Pages) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := chi.URLParam(r, "user")

			// if using a DID or handle, just continue as per usual
			if userutil.IsDid(user) || userutil.IsHandle(user) {
				next.ServeHTTP(w, r)
				return
			}

			// TODO: run Normalize middleware from here

			p.Error404(w)
			return
		})
	}
}
