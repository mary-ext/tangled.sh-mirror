package oauth

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/posthog/posthog-go"
)

func (o *OAuth) Router() http.Handler {
	r := chi.NewRouter()

	r.Get("/oauth/client-metadata.json", o.clientMetadata)
	r.Get("/oauth/jwks.json", o.jwks)
	r.Get("/oauth/callback", o.callback)
	return r
}

func (o *OAuth) clientMetadata(w http.ResponseWriter, r *http.Request) {
	doc := o.ClientApp.Config.ClientMetadata()
	doc.JWKSURI = &o.JwksUri

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (o *OAuth) jwks(w http.ResponseWriter, r *http.Request) {
	jwks := o.Config.OAuth.Jwks
	pubKey, err := pubKeyFromJwk(jwks)
	if err != nil {
		log.Printf("error parsing public key: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]any{
		"keys": []jwk.Key{pubKey},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (o *OAuth) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sessData, err := o.ClientApp.ProcessCallback(ctx, r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := o.SaveSession(w, r, sessData); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !o.Config.Core.Dev {
		err = o.Posthog.Enqueue(posthog.Capture{
			DistinctId: sessData.AccountDID.String(),
			Event:      "signin",
		})
		if err != nil {
			log.Println("failed to enqueue posthog event:", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusFound)
}
