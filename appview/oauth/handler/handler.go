package oauth

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/posthog/posthog-go"
	"tangled.sh/icyphox.sh/atproto-oauth/helpers"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/idresolver"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/oauth/client"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/rbac"
)

const (
	oauthScope = "atproto transition:generic"
)

type OAuthHandler struct {
	Config     *appview.Config
	Pages      *pages.Pages
	Idresolver *idresolver.Resolver
	Db         *db.DB
	Store      *sessions.CookieStore
	OAuth      *oauth.OAuth
	Enforcer   *rbac.Enforcer
	Posthog    posthog.Client
}

func (o *OAuthHandler) Router() http.Handler {
	r := chi.NewRouter()

	r.Get("/login", o.login)
	r.Post("/login", o.login)

	r.With(middleware.AuthMiddleware(o.OAuth)).Post("/logout", o.logout)

	r.Get("/oauth/client-metadata.json", o.clientMetadata)
	r.Get("/oauth/jwks.json", o.jwks)
	r.Get("/oauth/callback", o.callback)
	return r
}

func (o *OAuthHandler) clientMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(o.OAuth.ClientMetadata())
}

func (o *OAuthHandler) jwks(w http.ResponseWriter, r *http.Request) {
	jwks := o.Config.OAuth.Jwks
	pubKey, err := pubKeyFromJwk(jwks)
	if err != nil {
		log.Printf("error parsing public key: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := helpers.CreateJwksResponseObject(pubKey)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (o *OAuthHandler) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		o.Pages.Login(w, pages.LoginParams{})
	case http.MethodPost:
		handle := strings.TrimPrefix(r.FormValue("handle"), "@")

		resolved, err := o.Idresolver.ResolveIdent(r.Context(), handle)
		if err != nil {
			log.Println("failed to resolve handle:", err)
			o.Pages.Notice(w, "login-msg", fmt.Sprintf("\"%s\" is an invalid handle.", handle))
			return
		}
		self := o.OAuth.ClientMetadata()
		oauthClient, err := client.NewClient(
			self.ClientID,
			o.Config.OAuth.Jwks,
			self.RedirectURIs[0],
		)

		if err != nil {
			log.Println("failed to create oauth client:", err)
			o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		authServer, err := oauthClient.ResolvePdsAuthServer(r.Context(), resolved.PDSEndpoint())
		if err != nil {
			log.Println("failed to resolve auth server:", err)
			o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		authMeta, err := oauthClient.FetchAuthServerMetadata(r.Context(), authServer)
		if err != nil {
			log.Println("failed to fetch auth server metadata:", err)
			o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		dpopKey, err := helpers.GenerateKey(nil)
		if err != nil {
			log.Println("failed to generate dpop key:", err)
			o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		dpopKeyJson, err := json.Marshal(dpopKey)
		if err != nil {
			log.Println("failed to marshal dpop key:", err)
			o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		parResp, err := oauthClient.SendParAuthRequest(r.Context(), authServer, authMeta, handle, oauthScope, dpopKey)
		if err != nil {
			log.Println("failed to send par auth request:", err)
			o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		err = db.SaveOAuthRequest(o.Db, db.OAuthRequest{
			Did:                 resolved.DID.String(),
			PdsUrl:              resolved.PDSEndpoint(),
			Handle:              handle,
			AuthserverIss:       authMeta.Issuer,
			PkceVerifier:        parResp.PkceVerifier,
			DpopAuthserverNonce: parResp.DpopAuthserverNonce,
			DpopPrivateJwk:      string(dpopKeyJson),
			State:               parResp.State,
		})
		if err != nil {
			log.Println("failed to save oauth request:", err)
			o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		u, _ := url.Parse(authMeta.AuthorizationEndpoint)
		query := url.Values{}
		query.Add("client_id", self.ClientID)
		query.Add("request_uri", parResp.RequestUri)
		u.RawQuery = query.Encode()
		o.Pages.HxRedirect(w, u.String())
	}
}

func (o *OAuthHandler) callback(w http.ResponseWriter, r *http.Request) {
	state := r.FormValue("state")

	oauthRequest, err := db.GetOAuthRequestByState(o.Db, state)
	if err != nil {
		log.Println("failed to get oauth request:", err)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	defer func() {
		err := db.DeleteOAuthRequestByState(o.Db, state)
		if err != nil {
			log.Println("failed to delete oauth request for state:", state, err)
		}
	}()

	error := r.FormValue("error")
	errorDescription := r.FormValue("error_description")
	if error != "" || errorDescription != "" {
		log.Printf("error: %s, %s", error, errorDescription)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	code := r.FormValue("code")
	if code == "" {
		log.Println("missing code for state: ", state)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	iss := r.FormValue("iss")
	if iss == "" {
		log.Println("missing iss for state: ", state)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	self := o.OAuth.ClientMetadata()

	oauthClient, err := client.NewClient(
		self.ClientID,
		o.Config.OAuth.Jwks,
		self.RedirectURIs[0],
	)

	if err != nil {
		log.Println("failed to create oauth client:", err)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	jwk, err := helpers.ParseJWKFromBytes([]byte(oauthRequest.DpopPrivateJwk))
	if err != nil {
		log.Println("failed to parse jwk:", err)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	tokenResp, err := oauthClient.InitialTokenRequest(
		r.Context(),
		code,
		oauthRequest.AuthserverIss,
		oauthRequest.PkceVerifier,
		oauthRequest.DpopAuthserverNonce,
		jwk,
	)
	if err != nil {
		log.Println("failed to get token:", err)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	if tokenResp.Scope != oauthScope {
		log.Println("scope doesn't match:", tokenResp.Scope)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	err = o.OAuth.SaveSession(w, r, oauthRequest, tokenResp)
	if err != nil {
		log.Println("failed to save session:", err)
		o.Pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	log.Println("session saved successfully")
	go o.addToDefaultKnot(oauthRequest.Did)

	if !o.Config.Core.Dev {
		err = o.Posthog.Enqueue(posthog.Capture{
			DistinctId: oauthRequest.Did,
			Event:      "signin",
		})
		if err != nil {
			log.Println("failed to enqueue posthog event:", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func (o *OAuthHandler) logout(w http.ResponseWriter, r *http.Request) {
	err := o.OAuth.ClearSession(r, w)
	if err != nil {
		log.Println("failed to clear session:", err)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	log.Println("session cleared successfully")
	http.Redirect(w, r, "/", http.StatusFound)
}

func pubKeyFromJwk(jwks string) (jwk.Key, error) {
	k, err := helpers.ParseJWKFromBytes([]byte(jwks))
	if err != nil {
		return nil, err
	}
	pubKey, err := k.PublicKey()
	if err != nil {
		return nil, err
	}
	return pubKey, nil
}

func (o *OAuthHandler) addToDefaultKnot(did string) {
	defaultKnot := "knot1.tangled.sh"

	log.Printf("adding %s to default knot", did)
	err := o.Enforcer.AddMember(defaultKnot, did)
	if err != nil {
		log.Println("failed to add user to knot1.tangled.sh: ", err)
		return
	}
	err = o.Enforcer.E.SavePolicy()
	if err != nil {
		log.Println("failed to add user to knot1.tangled.sh: ", err)
		return
	}

	secret, err := db.GetRegistrationKey(o.Db, defaultKnot)
	if err != nil {
		log.Println("failed to get registration key for knot1.tangled.sh")
		return
	}
	signedClient, err := knotclient.NewSignedClient(defaultKnot, secret, o.Config.Core.Dev)
	resp, err := signedClient.AddMember(did)
	if err != nil {
		log.Println("failed to add user to knot1.tangled.sh: ", err)
		return
	}

	if resp.StatusCode != http.StatusNoContent {
		log.Println("failed to add user to knot1.tangled.sh: ", resp.StatusCode)
		return
	}
}
