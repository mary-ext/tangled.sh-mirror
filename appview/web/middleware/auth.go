package middleware

import (
	"fmt"
	"net/http"
	"net/url"

	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/session"
	"tangled.org/core/log"
)

// WithSession resumes atp session from cookie, ensure it's not malformed and
// pass the session through context
func WithSession(o *oauth.OAuth) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atSess, err := o.ResumeSession(r)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			sess := session.New(atSess)

			ctx := session.IntoContext(r.Context(), sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AuthMiddleware ensures the request is authorized and redirect to login page
// when unauthorized
func AuthMiddleware() middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			l := log.FromContext(ctx)

			returnURL := "/"
			if u, err := url.Parse(r.Header.Get("Referer")); err == nil {
				returnURL = u.RequestURI()
			}

			loginURL := fmt.Sprintf("/login?return_url=%s", url.QueryEscape(returnURL))

			redirectFunc := func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, loginURL, http.StatusTemporaryRedirect)
			}
			if r.Header.Get("HX-Request") == "true" {
				redirectFunc = func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("HX-Redirect", loginURL)
					w.WriteHeader(http.StatusOK)
				}
			}

			sess := session.FromContext(ctx)
			if sess == nil {
				l.Debug("no session, redirecting...")
				redirectFunc(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
