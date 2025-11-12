package middleware

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/state/userutil"
)

func Normalize() middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pat := chi.URLParam(r, "*")
			pathParts := strings.SplitN(pat, "/", 2)
			if len(pathParts) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			firstPart := pathParts[0]

			// if using a flattened DID (like you would in go modules), unflatten
			if userutil.IsFlattenedDid(firstPart) {
				unflattenedDid := userutil.UnflattenDid(firstPart)
				redirectPath := strings.Join(append([]string{unflattenedDid}, pathParts[1:]...), "/")

				redirectURL := *r.URL
				redirectURL.Path = "/" + redirectPath

				http.Redirect(w, r, redirectURL.String(), http.StatusFound)
				return
			}

			// if using a handle with @, rewrite to work without @
			if normalized := strings.TrimPrefix(firstPart, "@"); userutil.IsHandle(normalized) {
				redirectPath := strings.Join(append([]string{normalized}, pathParts[1:]...), "/")

				redirectURL := *r.URL
				redirectURL.Path = "/" + redirectPath

				http.Redirect(w, r, redirectURL.String(), http.StatusFound)
				return
			}

			next.ServeHTTP(w, r)
			return
		})
	}
}
