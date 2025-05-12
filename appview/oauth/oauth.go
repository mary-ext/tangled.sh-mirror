package oauth

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/sessions"
	oauth "github.com/haileyok/atproto-oauth-golang"
	"github.com/haileyok/atproto-oauth-golang/helpers"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/oauth/client"
	xrpc "tangled.sh/tangled.sh/core/appview/xrpcclient"
)

type OAuthRequest struct {
	ID                  uint
	AuthserverIss       string
	State               string
	Did                 string
	PdsUrl              string
	PkceVerifier        string
	DpopAuthserverNonce string
	DpopPrivateJwk      string
}

type OAuth struct {
	Store  *sessions.CookieStore
	Db     *db.DB
	Config *appview.Config
}

func NewOAuth(db *db.DB, config *appview.Config) *OAuth {
	return &OAuth{
		Store:  sessions.NewCookieStore([]byte(config.Core.CookieSecret)),
		Db:     db,
		Config: config,
	}
}

func (o *OAuth) SaveSession(w http.ResponseWriter, r *http.Request, oreq db.OAuthRequest, oresp *oauth.TokenResponse) error {
	// first we save the did in the user session
	userSession, err := o.Store.Get(r, appview.SessionName)
	if err != nil {
		return err
	}

	userSession.Values[appview.SessionDid] = oreq.Did
	userSession.Values[appview.SessionHandle] = oreq.Handle
	userSession.Values[appview.SessionPds] = oreq.PdsUrl
	userSession.Values[appview.SessionAuthenticated] = true
	err = userSession.Save(r, w)
	if err != nil {
		return fmt.Errorf("error saving user session: %w", err)
	}

	// then save the whole thing in the db
	session := db.OAuthSession{
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

	return db.SaveOAuthSession(o.Db, session)
}

func (o *OAuth) ClearSession(r *http.Request, w http.ResponseWriter) error {
	userSession, err := o.Store.Get(r, appview.SessionName)
	if err != nil || userSession.IsNew {
		return fmt.Errorf("error getting user session (or new session?): %w", err)
	}

	did := userSession.Values[appview.SessionDid].(string)

	err = db.DeleteOAuthSessionByDid(o.Db, did)
	if err != nil {
		return fmt.Errorf("error deleting oauth session: %w", err)
	}

	userSession.Options.MaxAge = -1

	return userSession.Save(r, w)
}

func (o *OAuth) GetSession(r *http.Request) (*db.OAuthSession, bool, error) {
	userSession, err := o.Store.Get(r, appview.SessionName)
	if err != nil || userSession.IsNew {
		return nil, false, fmt.Errorf("error getting user session (or new session?): %w", err)
	}

	did := userSession.Values[appview.SessionDid].(string)
	auth := userSession.Values[appview.SessionAuthenticated].(bool)

	session, err := db.GetOAuthSessionByDid(o.Db, did)
	if err != nil {
		return nil, false, fmt.Errorf("error getting oauth session: %w", err)
	}

	expiry, err := time.Parse(time.RFC3339, session.Expiry)
	if err != nil {
		return nil, false, fmt.Errorf("error parsing expiry time: %w", err)
	}
	if expiry.Sub(time.Now()) <= 5*time.Minute {
		privateJwk, err := helpers.ParseJWKFromBytes([]byte(session.DpopPrivateJwk))
		if err != nil {
			return nil, false, err
		}

		self := o.ClientMetadata()

		oauthClient, err := client.NewClient(
			self.ClientID,
			o.Config.OAuth.Jwks,
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
		err = db.RefreshOAuthSession(o.Db, did, resp.AccessToken, resp.RefreshToken, newExpiry)
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
	clientSession, err := a.Store.Get(r, appview.SessionName)

	if err != nil || clientSession.IsNew {
		return nil
	}

	return &User{
		Handle: clientSession.Values[appview.SessionHandle].(string),
		Did:    clientSession.Values[appview.SessionDid].(string),
		Pds:    clientSession.Values[appview.SessionPds].(string),
	}
}

func (a *OAuth) GetDid(r *http.Request) string {
	clientSession, err := a.Store.Get(r, appview.SessionName)

	if err != nil || clientSession.IsNew {
		return ""
	}

	return clientSession.Values[appview.SessionDid].(string)
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
			err := db.UpdateDpopPdsNonce(o.Db, did, newNonce)
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

	clientURI := o.Config.Core.AppviewHost
	clientID := fmt.Sprintf("%s/oauth/client-metadata.json", clientURI)
	redirectURIs := makeRedirectURIs(clientURI)

	if o.Config.Core.Dev {
		clientURI = fmt.Sprintf("http://127.0.0.1:3000")
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
