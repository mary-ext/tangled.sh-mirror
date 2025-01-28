package routes

import (
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
	rauth "github.com/icyphox/bild/legit/routes/auth"
)

const (
	layout = "2006-01-02 15:04:05.999999999 -0700 MST"
)

func (h *Handle) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := h.s.Get(r, "bild-session")
		auth, ok := session.Values["authenticated"].(bool)

		if !ok || !auth {
			log.Printf("not logged in, redirecting")
			http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			return
		}

		// refresh if nearing expiry
		// TODO: dedup with /login
		expiryStr := session.Values["expiry"].(string)
		expiry, _ := time.Parse(layout, expiryStr)
		pdsUrl := session.Values["pds"].(string)
		did := session.Values["did"].(string)
		refreshJwt := session.Values["refreshJwt"].(string)

		if time.Now().After((expiry)) {
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
				h.Write500(w)
				return
			}

			err = h.auth.StoreSession(r, w, nil, &rauth.AtSessionRefresh{ServerRefreshSession_Output: *atSession, PDSEndpoint: pdsUrl})
			if err != nil {
				log.Printf("failed to store session for did: %s\n: %s", atSession.Did, err)
				h.Write500(w)
				return
			}

			log.Println("successfully refreshed token")
		}

		if r.URL.Path == "/login" {
			log.Println("already logged in")
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			return
		}

		next.ServeHTTP(w, r)
	})
}
