package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/gorilla/sessions"
)

type Auth struct {
	s sessions.Store
}

func NewAuth(store sessions.Store) *Auth {
	return &Auth{store}
}

func resolveIdent(ctx context.Context, arg string) (*identity.Identity, error) {
	id, err := syntax.ParseAtIdentifier(arg)
	if err != nil {
		return nil, err
	}

	dir := identity.DefaultDirectory()
	return dir.Lookup(ctx, *id)
}

func (a *Auth) CreateInitialSession(w http.ResponseWriter, r *http.Request, username, appPassword string) (AtSessionCreate, error) {
	ctx := r.Context()
	resolved, err := resolveIdent(ctx, username)
	if err != nil {
		return AtSessionCreate{}, fmt.Errorf("invalid handle: %s", err)
	}

	pdsUrl := resolved.PDSEndpoint()
	client := xrpc.Client{
		Host: pdsUrl,
	}

	atSession, err := comatproto.ServerCreateSession(ctx, &client, &comatproto.ServerCreateSession_Input{
		Identifier: resolved.DID.String(),
		Password:   appPassword,
	})
	if err != nil {
		return AtSessionCreate{}, fmt.Errorf("invalid app password")
	}

	return AtSessionCreate{
		ServerCreateSession_Output: *atSession,
		PDSEndpoint:                pdsUrl,
	}, nil
}

func (a *Auth) StoreSession(r *http.Request, w http.ResponseWriter, atSessionCreate *AtSessionCreate, atSessionRefresh *AtSessionRefresh) error {
	if atSessionCreate != nil {
		atSession := atSessionCreate

		clientSession, _ := a.s.Get(r, "bild-session")
		clientSession.Values["handle"] = atSession.Handle
		clientSession.Values["did"] = atSession.Did
		clientSession.Values["accessJwt"] = atSession.AccessJwt
		clientSession.Values["refreshJwt"] = atSession.RefreshJwt
		clientSession.Values["expiry"] = time.Now().Add(time.Hour).String()
		clientSession.Values["pds"] = atSession.PDSEndpoint
		clientSession.Values["authenticated"] = true

		return clientSession.Save(r, w)
	} else {
		atSession := atSessionRefresh

		clientSession, _ := a.s.Get(r, "bild-session")
		clientSession.Values["handle"] = atSession.Handle
		clientSession.Values["did"] = atSession.Did
		clientSession.Values["accessJwt"] = atSession.AccessJwt
		clientSession.Values["refreshJwt"] = atSession.RefreshJwt
		clientSession.Values["expiry"] = time.Now().Add(time.Hour).String()
		clientSession.Values["pds"] = atSession.PDSEndpoint
		clientSession.Values["authenticated"] = true

		return clientSession.Save(r, w)
	}
}

func (a *Auth) GetSessionUser(r *http.Request) (*identity.Identity, error) {
	session, _ := a.s.Get(r, "bild-session")
	did, ok := session.Values["did"].(string)
	if !ok {
		return nil, fmt.Errorf("user is not authenticated")
	}

	return resolveIdent(r.Context(), did)
}
