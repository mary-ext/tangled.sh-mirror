package handler

import (
	"encoding/json"
	"net/http"

	"tangled.org/core/appview/oauth"
)

func OauthJwks(o *oauth.OAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := o.ClientApp.Config.PublicJWKS()
		if err := json.NewEncoder(w).Encode(body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}
