package routes

import (
	"github.com/go-chi/chi/v5"
	"github.com/icyphox/bild/db"
	auth "github.com/icyphox/bild/routes/auth"
	"log"
	"net/http"
)

func (h *Handle) AccessLevel(level db.Level) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			repoOwnerHandle := chi.URLParam(r, "user")
			repoOwner, err := auth.ResolveIdent(r.Context(), repoOwnerHandle)
			if err != nil {
				log.Println("invalid did")
				http.Error(w, "invalid did", http.StatusNotFound)
				return
			}
			repoName := chi.URLParam(r, "name")
			session, _ := h.s.Get(r, "bild-session")
			did := session.Values["did"].(string)

			userLevel, err := h.db.GetAccessLevel(did, repoOwner.DID.String(), repoName)
			if err != nil || userLevel < level {
				log.Printf("unauthorized access: %s accessing %s/%s\n", did, repoOwnerHandle, repoName)
				log.Printf("wanted level: %s, got level %s", level.String(), userLevel.String())
				http.Error(w, "Forbidden", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
