package middleware

import (
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/auth"
)

type Middleware func(http.Handler) http.Handler

func AuthMiddleware(a *auth.Auth) Middleware {
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

			session, err := a.GetSession(r)
			if session.IsNew || err != nil {
				log.Printf("not logged in, redirecting")
				redirectFunc(w, r)
				return
			}

			authorized, ok := session.Values[appview.SessionAuthenticated].(bool)
			if !ok || !authorized {
				log.Printf("not logged in, redirecting")
				redirectFunc(w, r)
				return
			}

			// refresh if nearing expiry
			// TODO: dedup with /login
			expiryStr := session.Values[appview.SessionExpiry].(string)
			expiry, err := time.Parse(time.RFC3339, expiryStr)
			if err != nil {
				log.Println("invalid expiry time", err)
				redirectFunc(w, r)
				return
			}
			pdsUrl, ok1 := session.Values[appview.SessionPds].(string)
			did, ok2 := session.Values[appview.SessionDid].(string)
			refreshJwt, ok3 := session.Values[appview.SessionRefreshJwt].(string)

			if !ok1 || !ok2 || !ok3 {
				log.Println("invalid expiry time", err)
				redirectFunc(w, r)
				return
			}

			if time.Now().After(expiry) {
				log.Println("token expired, refreshing ...")

				client := xrpc.Client{
					Host: pdsUrl,
					Auth: &xrpc.AuthInfo{
						Did:        did,
						AccessJwt:  refreshJwt,
						RefreshJwt: refreshJwt,
					},
				}
				atSession, err := comatproto.ServerRefreshSession(r.Context(), &client)
				if err != nil {
					log.Println("failed to refresh session", err)
					redirectFunc(w, r)
					return
				}

				sessionish := auth.RefreshSessionWrapper{atSession}

				err = a.StoreSession(r, w, &sessionish, pdsUrl)
				if err != nil {
					log.Printf("failed to store session for did: %s\n: %s", atSession.Did, err)
					return
				}

				log.Println("successfully refreshed token")
			}

			next.ServeHTTP(w, r)
		})
	}
}
