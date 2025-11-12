package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"tangled.org/core/appview/oauth"
)

func AuthMiddleware(o *oauth.OAuth) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

			sess, err := o.ResumeSession(r)
			if err != nil {
				log.Println("failed to resume session, redirecting...", "err", err, "url", r.URL.String())
				redirectFunc(w, r)
				return
			}

			if sess == nil {
				log.Printf("session is nil, redirecting...")
				redirectFunc(w, r)
				return
			}

			// TODO: use IntoContext instead
			ctx := context.WithValue(r.Context(), "sess", sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
