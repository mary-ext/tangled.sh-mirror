package routes

import (
	"net/http"
)

func (h *Handle) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := h.s.Get(r, "bild-session")
		auth, ok := session.Values["authenticated"].(bool)
		if !ok || !auth {
			http.Error(w, "Forbidden: You are not logged in", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
