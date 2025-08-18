package oauth

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	indigo_xrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/gorilla/sessions"
	oauth "tangled.sh/icyphox.sh/atproto-oauth"
	"tangled.sh/icyphox.sh/atproto-oauth/helpers"
	sessioncache "tangled.sh/tangled.sh/core/appview/cache/session"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/oauth/client"
	xrpc "tangled.sh/tangled.sh/core/appview/xrpcclient"
)

type OAuth struct {
	store  *sessions.CookieStore
	config *config.Config
	sess   *sessioncache.SessionStore
}

func NewOAuth(config *config.Config, sess *sessioncache.SessionStore) *OAuth {
	return &OAuth{
		store:  sessions.NewCookieStore([]byte(config.Core.CookieSecret)),
		config: config,
		sess:   sess,
	}
}

func (o *OAuth) Stores() *sessions.CookieStore {
	return o.store
}

func (o *OAuth) SaveSession(w http.ResponseWriter, r *http.Request, oreq sessioncache.OAuthRequest, oresp *oauth.TokenResponse) error {
	// first we save the did in the user session
	userSession, err := o.store.Get(r, SessionName)
	if err != nil {
		return err
	}

	userSession.Values[SessionDid] = oreq.Did
	userSession.Values[SessionHandle] = oreq.Handle
	userSession.Values[SessionPds] = oreq.PdsUrl
	userSession.Values[SessionAuthenticated] = true
	err = userSession.Save(r, w)
	if err != nil {
		return fmt.Errorf("error saving user session: %w", err)
	}

	// then save the whole thing in the db
	session := sessioncache.OAuthSession{
		Did:                 oreq.Did,
		Handle:              oreq.Handle,
		PdsUrl:              oreq.PdsUrl,
		DpopAuthserverNonce: oreq.DpopAuthserverNonce,
		AuthServerIss:       oreq.AuthserverIss,
		DpopPrivateJwk:      oreq.DpopPrivateJwk,
		AccessJwt:           oresp.AccessToken,
		RefreshJwt:          oresp.RefreshToken,
		Expiry:              time.Now().Add(time.Duration(oresp.ExpiresIn) * time.Second).Format(time.RFC3339),
	}

	return o.sess.SaveSession(r.Context(), session)
}

func (o *OAuth) ClearSession(r *http.Request, w http.ResponseWriter) error {
	userSession, err := o.store.Get(r, SessionName)
	if err != nil || userSession.IsNew {
		return fmt.Errorf("error getting user session (or new session?): %w", err)
	}

	did := userSession.Values[SessionDid].(string)

	err = o.sess.DeleteSession(r.Context(), did)
	if err != nil {
		return fmt.Errorf("error deleting oauth session: %w", err)
	}

	userSession.Options.MaxAge = -1

	return userSession.Save(r, w)
}

func (o *OAuth) GetSession(r *http.Request) (*sessioncache.OAuthSession, bool, error) {
	userSession, err := o.store.Get(r, SessionName)
	if err != nil || userSession.IsNew {
		return nil, false, fmt.Errorf("error getting user session (or new session?): %w", err)
	}

	did := userSession.Values[SessionDid].(string)
	auth := userSession.Values[SessionAuthenticated].(bool)

	session, err := o.sess.GetSession(r.Context(), did)
	if err != nil {
		return nil, false, fmt.Errorf("error getting oauth session: %w", err)
	}

	expiry, err := time.Parse(time.RFC3339, session.Expiry)
	if err != nil {
		return nil, false, fmt.Errorf("error parsing expiry time: %w", err)
	}
	if time.Until(expiry) <= 5*time.Minute {
		privateJwk, err := helpers.ParseJWKFromBytes([]byte(session.DpopPrivateJwk))
		if err != nil {
			return nil, false, err
		}

		self := o.ClientMetadata()

		oauthClient, err := client.NewClient(
			self.ClientID,
			o.config.OAuth.Jwks,
			self.RedirectURIs[0],
		)

		if err != nil {
			return nil, false, err
		}

		resp, err := oauthClient.RefreshTokenRequest(r.Context(), session.RefreshJwt, session.AuthServerIss, session.DpopAuthserverNonce, privateJwk)
		if err != nil {
			return nil, false, err
		}

		newExpiry := time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second).Format(time.RFC3339)
		err = o.sess.RefreshSession(r.Context(), did, resp.AccessToken, resp.RefreshToken, newExpiry)
		if err != nil {
			return nil, false, fmt.Errorf("error refreshing oauth session: %w", err)
		}

		// update the current session
		session.AccessJwt = resp.AccessToken
		session.RefreshJwt = resp.RefreshToken
		session.DpopAuthserverNonce = resp.DpopAuthserverNonce
		session.Expiry = newExpiry
	}

	return session, auth, nil
}

type User struct {
	Handle string
	Did    string
	Pds    string
}

func (a *OAuth) GetUser(r *http.Request) *User {
	clientSession, err := a.store.Get(r, SessionName)

	if err != nil || clientSession.IsNew {
		return nil
	}

	return &User{
		Handle: clientSession.Values[SessionHandle].(string),
		Did:    clientSession.Values[SessionDid].(string),
		Pds:    clientSession.Values[SessionPds].(string),
	}
}

func (a *OAuth) GetDid(r *http.Request) string {
	clientSession, err := a.store.Get(r, SessionName)

	if err != nil || clientSession.IsNew {
		return ""
	}

	return clientSession.Values[SessionDid].(string)
}

func (o *OAuth) AuthorizedClient(r *http.Request) (*xrpc.Client, error) {
	session, auth, err := o.GetSession(r)
	if err != nil {
		return nil, fmt.Errorf("error getting session: %w", err)
	}
	if !auth {
		return nil, fmt.Errorf("not authorized")
	}

	client := &oauth.XrpcClient{
		OnDpopPdsNonceChanged: func(did, newNonce string) {
			err := o.sess.UpdateNonce(r.Context(), did, newNonce)
			if err != nil {
				log.Printf("error updating dpop pds nonce: %v", err)
			}
		},
	}

	privateJwk, err := helpers.ParseJWKFromBytes([]byte(session.DpopPrivateJwk))
	if err != nil {
		return nil, fmt.Errorf("error parsing private jwk: %w", err)
	}

	xrpcClient := xrpc.NewClient(client, &oauth.XrpcAuthedRequestArgs{
		Did:            session.Did,
		PdsUrl:         session.PdsUrl,
		DpopPdsNonce:   session.PdsUrl,
		AccessToken:    session.AccessJwt,
		Issuer:         session.AuthServerIss,
		DpopPrivateJwk: privateJwk,
	})

	return xrpcClient, nil
}

// use this to create a client to communicate with knots or spindles
//
// this is a higher level abstraction on ServerGetServiceAuth
type ServiceClientOpts struct {
	service string
	exp     int64
	lxm     string
	dev     bool
}

type ServiceClientOpt func(*ServiceClientOpts)

func WithService(service string) ServiceClientOpt {
	return func(s *ServiceClientOpts) {
		s.service = service
	}
}

// Specify the Duration in seconds for the expiry of this token
//
// The time of expiry is calculated as time.Now().Unix() + exp
func WithExp(exp int64) ServiceClientOpt {
	return func(s *ServiceClientOpts) {
		s.exp = time.Now().Unix() + exp
	}
}

func WithLxm(lxm string) ServiceClientOpt {
	return func(s *ServiceClientOpts) {
		s.lxm = lxm
	}
}

func WithDev(dev bool) ServiceClientOpt {
	return func(s *ServiceClientOpts) {
		s.dev = dev
	}
}

func (s *ServiceClientOpts) Audience() string {
	return fmt.Sprintf("did:web:%s", s.service)
}

func (s *ServiceClientOpts) Host() string {
	scheme := "https://"
	if s.dev {
		scheme = "http://"
	}

	return scheme + s.service
}

func (o *OAuth) ServiceClient(r *http.Request, os ...ServiceClientOpt) (*indigo_xrpc.Client, error) {
	opts := ServiceClientOpts{}
	for _, o := range os {
		o(&opts)
	}

	authorizedClient, err := o.AuthorizedClient(r)
	if err != nil {
		return nil, err
	}

	// force expiry to atleast 60 seconds in the future
	sixty := time.Now().Unix() + 60
	if opts.exp < sixty {
		opts.exp = sixty
	}

	resp, err := authorizedClient.ServerGetServiceAuth(r.Context(), opts.Audience(), opts.exp, opts.lxm)
	if err != nil {
		return nil, err
	}

	return &indigo_xrpc.Client{
		Auth: &indigo_xrpc.AuthInfo{
			AccessJwt: resp.Token,
		},
		Host: opts.Host(),
		Client: &http.Client{
			Timeout: time.Second * 5,
		},
	}, nil
}

type ClientMetadata struct {
	ClientID                    string   `json:"client_id"`
	ClientName                  string   `json:"client_name"`
	SubjectType                 string   `json:"subject_type"`
	ClientURI                   string   `json:"client_uri"`
	RedirectURIs                []string `json:"redirect_uris"`
	GrantTypes                  []string `json:"grant_types"`
	ResponseTypes               []string `json:"response_types"`
	ApplicationType             string   `json:"application_type"`
	DpopBoundAccessTokens       bool     `json:"dpop_bound_access_tokens"`
	JwksURI                     string   `json:"jwks_uri"`
	Scope                       string   `json:"scope"`
	TokenEndpointAuthMethod     string   `json:"token_endpoint_auth_method"`
	TokenEndpointAuthSigningAlg string   `json:"token_endpoint_auth_signing_alg"`
}

func (o *OAuth) ClientMetadata() ClientMetadata {
	makeRedirectURIs := func(c string) []string {
		return []string{fmt.Sprintf("%s/oauth/callback", c)}
	}

	clientURI := o.config.Core.AppviewHost
	clientID := fmt.Sprintf("%s/oauth/client-metadata.json", clientURI)
	redirectURIs := makeRedirectURIs(clientURI)

	if o.config.Core.Dev {
		clientURI = "http://127.0.0.1:3000"
		redirectURIs = makeRedirectURIs(clientURI)

		query := url.Values{}
		query.Add("redirect_uri", redirectURIs[0])
		query.Add("scope", "atproto transition:generic")
		clientID = fmt.Sprintf("http://localhost?%s", query.Encode())
	}

	jwksURI := fmt.Sprintf("%s/oauth/jwks.json", clientURI)

	return ClientMetadata{
		ClientID:                    clientID,
		ClientName:                  "Tangled",
		SubjectType:                 "public",
		ClientURI:                   clientURI,
		RedirectURIs:                redirectURIs,
		GrantTypes:                  []string{"authorization_code", "refresh_token"},
		ResponseTypes:               []string{"code"},
		ApplicationType:             "web",
		DpopBoundAccessTokens:       true,
		JwksURI:                     jwksURI,
		Scope:                       "atproto transition:generic",
		TokenEndpointAuthMethod:     "private_key_jwt",
		TokenEndpointAuthSigningAlg: "ES256",
	}
}
