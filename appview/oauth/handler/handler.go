package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/posthog/posthog-go"
	"tangled.sh/icyphox.sh/atproto-oauth/helpers"
	tangled "tangled.sh/tangled.sh/core/api/tangled"
	sessioncache "tangled.sh/tangled.sh/core/appview/cache/session"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/middleware"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/oauth/client"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/tid"
)

const (
	oauthScope = "atproto transition:generic"
)

type OAuthHandler struct {
	config     *config.Config
	pages      *pages.Pages
	idResolver *idresolver.Resolver
	sess       *sessioncache.SessionStore
	db         *db.DB
	store      *sessions.CookieStore
	oauth      *oauth.OAuth
	enforcer   *rbac.Enforcer
	posthog    posthog.Client
}

func New(
	config *config.Config,
	pages *pages.Pages,
	idResolver *idresolver.Resolver,
	db *db.DB,
	sess *sessioncache.SessionStore,
	store *sessions.CookieStore,
	oauth *oauth.OAuth,
	enforcer *rbac.Enforcer,
	posthog posthog.Client,
) *OAuthHandler {
	return &OAuthHandler{
		config:     config,
		pages:      pages,
		idResolver: idResolver,
		db:         db,
		sess:       sess,
		store:      store,
		oauth:      oauth,
		enforcer:   enforcer,
		posthog:    posthog,
	}
}

func (o *OAuthHandler) Router() http.Handler {
	r := chi.NewRouter()

	r.Get("/login", o.login)
	r.Post("/login", o.login)

	r.With(middleware.AuthMiddleware(o.oauth)).Post("/logout", o.logout)

	r.Get("/oauth/client-metadata.json", o.clientMetadata)
	r.Get("/oauth/jwks.json", o.jwks)
	r.Get("/oauth/callback", o.callback)
	return r
}

func (o *OAuthHandler) clientMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(o.oauth.ClientMetadata())
}

func (o *OAuthHandler) jwks(w http.ResponseWriter, r *http.Request) {
	jwks := o.config.OAuth.Jwks
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
		returnURL := r.URL.Query().Get("return_url")
		o.pages.Login(w, pages.LoginParams{
			ReturnUrl: returnURL,
		})
	case http.MethodPost:
		handle := r.FormValue("handle")

		// when users copy their handle from bsky.app, it tends to have these characters around it:
		//
		// @nelind.dk:
		//   \u202a ensures that the handle is always rendered left to right and
		//   \u202c reverts that so the rest of the page renders however it should
		handle = strings.TrimPrefix(handle, "\u202a")
		handle = strings.TrimSuffix(handle, "\u202c")

		// `@` is harmless
		handle = strings.TrimPrefix(handle, "@")

		// basic handle validation
		if !strings.Contains(handle, ".") {
			log.Println("invalid handle format", "raw", handle)
			o.pages.Notice(w, "login-msg", fmt.Sprintf("\"%s\" is an invalid handle. Did you mean %s.bsky.social?", handle, handle))
			return
		}

		resolved, err := o.idResolver.ResolveIdent(r.Context(), handle)
		if err != nil {
			log.Println("failed to resolve handle:", err)
			o.pages.Notice(w, "login-msg", fmt.Sprintf("\"%s\" is an invalid handle.", handle))
			return
		}
		self := o.oauth.ClientMetadata()
		oauthClient, err := client.NewClient(
			self.ClientID,
			o.config.OAuth.Jwks,
			self.RedirectURIs[0],
		)

		if err != nil {
			log.Println("failed to create oauth client:", err)
			o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		authServer, err := oauthClient.ResolvePdsAuthServer(r.Context(), resolved.PDSEndpoint())
		if err != nil {
			log.Println("failed to resolve auth server:", err)
			o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		authMeta, err := oauthClient.FetchAuthServerMetadata(r.Context(), authServer)
		if err != nil {
			log.Println("failed to fetch auth server metadata:", err)
			o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		dpopKey, err := helpers.GenerateKey(nil)
		if err != nil {
			log.Println("failed to generate dpop key:", err)
			o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		dpopKeyJson, err := json.Marshal(dpopKey)
		if err != nil {
			log.Println("failed to marshal dpop key:", err)
			o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		parResp, err := oauthClient.SendParAuthRequest(r.Context(), authServer, authMeta, handle, oauthScope, dpopKey)
		if err != nil {
			log.Println("failed to send par auth request:", err)
			o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		err = o.sess.SaveRequest(r.Context(), sessioncache.OAuthRequest{
			Did:                 resolved.DID.String(),
			PdsUrl:              resolved.PDSEndpoint(),
			Handle:              handle,
			AuthserverIss:       authMeta.Issuer,
			PkceVerifier:        parResp.PkceVerifier,
			DpopAuthserverNonce: parResp.DpopAuthserverNonce,
			DpopPrivateJwk:      string(dpopKeyJson),
			State:               parResp.State,
			ReturnUrl:           r.FormValue("return_url"),
		})
		if err != nil {
			log.Println("failed to save oauth request:", err)
			o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
			return
		}

		u, _ := url.Parse(authMeta.AuthorizationEndpoint)
		query := url.Values{}
		query.Add("client_id", self.ClientID)
		query.Add("request_uri", parResp.RequestUri)
		u.RawQuery = query.Encode()
		o.pages.HxRedirect(w, u.String())
	}
}

func (o *OAuthHandler) callback(w http.ResponseWriter, r *http.Request) {
	state := r.FormValue("state")

	oauthRequest, err := o.sess.GetRequestByState(r.Context(), state)
	if err != nil {
		log.Println("failed to get oauth request:", err)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	defer func() {
		err := o.sess.DeleteRequestByState(r.Context(), state)
		if err != nil {
			log.Println("failed to delete oauth request for state:", state, err)
		}
	}()

	error := r.FormValue("error")
	errorDescription := r.FormValue("error_description")
	if error != "" || errorDescription != "" {
		log.Printf("error: %s, %s", error, errorDescription)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	code := r.FormValue("code")
	if code == "" {
		log.Println("missing code for state: ", state)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	iss := r.FormValue("iss")
	if iss == "" {
		log.Println("missing iss for state: ", state)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	if iss != oauthRequest.AuthserverIss {
		log.Println("mismatched iss:", iss, "!=", oauthRequest.AuthserverIss, "for state:", state)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	self := o.oauth.ClientMetadata()

	oauthClient, err := client.NewClient(
		self.ClientID,
		o.config.OAuth.Jwks,
		self.RedirectURIs[0],
	)

	if err != nil {
		log.Println("failed to create oauth client:", err)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	jwk, err := helpers.ParseJWKFromBytes([]byte(oauthRequest.DpopPrivateJwk))
	if err != nil {
		log.Println("failed to parse jwk:", err)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
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
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	if tokenResp.Scope != oauthScope {
		log.Println("scope doesn't match:", tokenResp.Scope)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	err = o.oauth.SaveSession(w, r, *oauthRequest, tokenResp)
	if err != nil {
		log.Println("failed to save session:", err)
		o.pages.Notice(w, "login-msg", "Failed to authenticate. Try again later.")
		return
	}

	log.Println("session saved successfully")
	go o.addToDefaultKnot(oauthRequest.Did)
	go o.addToDefaultSpindle(oauthRequest.Did)

	if !o.config.Core.Dev {
		err = o.posthog.Enqueue(posthog.Capture{
			DistinctId: oauthRequest.Did,
			Event:      "signin",
		})
		if err != nil {
			log.Println("failed to enqueue posthog event:", err)
		}
	}

	returnUrl := oauthRequest.ReturnUrl
	if returnUrl == "" {
		returnUrl = "/"
	}

	http.Redirect(w, r, returnUrl, http.StatusFound)
}

func (o *OAuthHandler) logout(w http.ResponseWriter, r *http.Request) {
	err := o.oauth.ClearSession(r, w)
	if err != nil {
		log.Println("failed to clear session:", err)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	log.Println("session cleared successfully")
	o.pages.HxRedirect(w, "/login")
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

var (
	tangledHandle  = "tangled.sh"
	tangledDid     = "did:plc:wshs7t2adsemcrrd4snkeqli"
	defaultSpindle = "spindle.tangled.sh"
	defaultKnot    = "knot1.tangled.sh"
)

func (o *OAuthHandler) addToDefaultSpindle(did string) {
	// use the tangled.sh app password to get an accessJwt
	// and create an sh.tangled.spindle.member record with that
	spindleMembers, err := db.GetSpindleMembers(
		o.db,
		db.FilterEq("instance", "spindle.tangled.sh"),
		db.FilterEq("subject", did),
	)
	if err != nil {
		log.Printf("failed to get spindle members for did %s: %v", did, err)
		return
	}

	if len(spindleMembers) != 0 {
		log.Printf("did %s is already a member of the default spindle", did)
		return
	}

	log.Printf("adding %s to default spindle", did)
	session, err := o.createAppPasswordSession()
	if err != nil {
		log.Printf("failed to create session: %s", err)
		return
	}

	record := tangled.SpindleMember{
		LexiconTypeID: "sh.tangled.spindle.member",
		Subject:       did,
		Instance:      defaultSpindle,
		CreatedAt:     time.Now().Format(time.RFC3339),
	}

	if err := session.putRecord(record); err != nil {
		log.Printf("failed to add member to default knot: %s", err)
		return
	}

	log.Printf("successfully added %s to default spindle", did)
}

func (o *OAuthHandler) addToDefaultKnot(did string) {
	// use the tangled.sh app password to get an accessJwt
	// and create an sh.tangled.spindle.member record with that

	allKnots, err := o.enforcer.GetKnotsForUser(did)
	if err != nil {
		log.Printf("failed to get knot members for did %s: %v", did, err)
		return
	}

	if slices.Contains(allKnots, defaultKnot) {
		log.Printf("did %s is already a member of the default knot", did)
		return
	}

	log.Printf("adding %s to default knot", did)
	session, err := o.createAppPasswordSession()
	if err != nil {
		log.Printf("failed to create session: %s", err)
		return
	}

	record := tangled.KnotMember{
		LexiconTypeID: "sh.tangled.knot.member",
		Subject:       did,
		Domain:        defaultKnot,
		CreatedAt:     time.Now().Format(time.RFC3339),
	}

	if err := session.putRecord(record); err != nil {
		log.Printf("failed to add member to default knot: %s", err)
		return
	}

	log.Printf("successfully added %s to default Knot", did)
}

// create a session using apppasswords
type session struct {
	AccessJwt   string `json:"accessJwt"`
	PdsEndpoint string
}

func (o *OAuthHandler) createAppPasswordSession() (*session, error) {
	appPassword := o.config.Core.AppPassword
	if appPassword == "" {
		return nil, fmt.Errorf("no app password configured, skipping member addition")
	}

	resolved, err := o.idResolver.ResolveIdent(context.Background(), tangledDid)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tangled.sh DID %s: %v", tangledDid, err)
	}

	pdsEndpoint := resolved.PDSEndpoint()
	if pdsEndpoint == "" {
		return nil, fmt.Errorf("no PDS endpoint found for tangled.sh DID %s", tangledDid)
	}

	sessionPayload := map[string]string{
		"identifier": tangledHandle,
		"password":   appPassword,
	}
	sessionBytes, err := json.Marshal(sessionPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session payload: %v", err)
	}

	sessionURL := pdsEndpoint + "/xrpc/com.atproto.server.createSession"
	sessionReq, err := http.NewRequestWithContext(context.Background(), "POST", sessionURL, bytes.NewBuffer(sessionBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create session request: %v", err)
	}
	sessionReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	sessionResp, err := client.Do(sessionReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %v", err)
	}
	defer sessionResp.Body.Close()

	if sessionResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to create session: HTTP %d", sessionResp.StatusCode)
	}

	var session session
	if err := json.NewDecoder(sessionResp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("failed to decode session response: %v", err)
	}

	session.PdsEndpoint = pdsEndpoint

	return &session, nil
}

func (s *session) putRecord(record any) error {
	recordBytes, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal knot member record: %w", err)
	}

	payload := map[string]any{
		"repo":       tangledDid,
		"collection": tangled.KnotMemberNSID,
		"rkey":       tid.TID(),
		"record":     json.RawMessage(recordBytes),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request payload: %w", err)
	}

	url := s.PdsEndpoint + "/xrpc/com.atproto.repo.putRecord"
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.AccessJwt)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to add user to default Knot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to add user to default Knot: HTTP %d", resp.StatusCode)
	}

	return nil
}
