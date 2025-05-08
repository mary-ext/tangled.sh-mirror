package oauth

import (
	"fmt"
	"log"
	"net/http"
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
		oauthClient, err := client.NewClient(o.Config.OAuth.ServerMetadataUrl,
			o.Config.OAuth.Jwks,
			fmt.Sprintf("%s/oauth/callback", o.Config.Core.AppviewHost))

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
