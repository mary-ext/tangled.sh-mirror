package routes

import (
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
)

const (
	layout = "2006-01-02 15:04:05.999999999 -0700 MST"
)

func (h *Handle) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := h.s.Get(r, "bild-session")
		auth, ok := session.Values["authenticated"].(bool)

		if !ok || !auth {
			http.Error(w, "Forbidden: You are not logged in", http.StatusForbidden)
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
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			clientSession, _ := h.s.Get(r, "bild-session")
			clientSession.Values["handle"] = atSession.Handle
			clientSession.Values["did"] = atSession.Did
			clientSession.Values["accessJwt"] = atSession.AccessJwt
			clientSession.Values["refreshJwt"] = atSession.RefreshJwt
			clientSession.Values["expiry"] = time.Now().Add(time.Hour).String()
			clientSession.Values["pds"] = pdsUrl
			clientSession.Values["authenticated"] = true

			err = clientSession.Save(r, w)

			if err != nil {
				log.Printf("failed to store session for did: %s\n", atSession.Did)
				log.Println(err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			log.Println("successfully refreshed token")
		}

		next.ServeHTTP(w, r)
	})
}
