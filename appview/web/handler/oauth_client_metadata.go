package handler

import (
	"encoding/json"
	"net/http"

	"tangled.org/core/appview/oauth"
)

func OauthClientMetadata(o *oauth.OAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc := o.ClientApp.Config.ClientMetadata()
		doc.JWKSURI = &o.JwksUri
		doc.ClientName = &o.ClientName
		doc.ClientURI = &o.ClientUri

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(doc); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}
