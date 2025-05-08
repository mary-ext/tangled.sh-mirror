package middleware

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pagination"
)

type Middleware func(http.Handler) http.Handler

func AuthMiddleware(a *oauth.OAuth) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redirectFunc := func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			}
			if r.Header.Get("HX-Request") == "true" {
				redirectFunc = func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("HX-Redirect", "/login")
					w.WriteHeader(http.StatusOK)
				}
			}

			_, auth, err := a.GetSession(r)
			if err != nil {
				log.Printf("not logged in, redirecting")
				redirectFunc(w, r)
				return
			}

			if !auth {
				log.Printf("not logged in, redirecting")
				redirectFunc(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func Paginate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := pagination.FirstPage()

		offsetVal := r.URL.Query().Get("offset")
		if offsetVal != "" {
			offset, err := strconv.Atoi(offsetVal)
			if err != nil {
				log.Println("invalid offset")
			} else {
				page.Offset = offset
			}
		}

		limitVal := r.URL.Query().Get("limit")
		if limitVal != "" {
			limit, err := strconv.Atoi(limitVal)
			if err != nil {
				log.Println("invalid limit")
			} else {
				page.Limit = limit
			}
		}

		ctx := context.WithValue(r.Context(), "page", page)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
