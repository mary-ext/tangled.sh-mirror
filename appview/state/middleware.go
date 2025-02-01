package state

import (
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/icyphox/bild/appview"
	"github.com/icyphox/bild/appview/auth"
)

type Middleware func(http.Handler) http.Handler

func AuthMiddleware(s *State) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := s.auth.Store.Get(r, appview.SESSION_NAME)
			authorized, ok := session.Values[appview.SESSION_AUTHENTICATED].(bool)

			if !ok || !authorized {
				log.Printf("not logged in, redirecting")
				http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
				return
			}

			// refresh if nearing expiry
			// TODO: dedup with /login
			expiryStr := session.Values[appview.SESSION_EXPIRY].(string)
			expiry, err := time.Parse(appview.TIME_LAYOUT, expiryStr)
			if err != nil {
				log.Println("invalid expiry time", err)
				return
			}
			pdsUrl := session.Values[appview.SESSION_PDS].(string)
			did := session.Values[appview.SESSION_DID].(string)
			refreshJwt := session.Values[appview.SESSION_REFRESHJWT].(string)

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
					log.Println(err)
					return
				}

				sessionish := auth.RefreshSessionWrapper{atSession}

				err = s.auth.StoreSession(r, w, &sessionish, pdsUrl)
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
