package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/gorilla/sessions"
	"github.com/icyphox/bild/appview"
)

type Auth struct {
	Store *sessions.CookieStore
}

type AtSessionCreate struct {
	comatproto.ServerCreateSession_Output
	PDSEndpoint string
}

type AtSessionRefresh struct {
	comatproto.ServerRefreshSession_Output
	PDSEndpoint string
}

func Make() (*Auth, error) {
	store := sessions.NewCookieStore([]byte(appview.SESSION_COOKIE_SECRET))
	return &Auth{store}, nil
}

func ResolveIdent(ctx context.Context, arg string) (*identity.Identity, error) {
	id, err := syntax.ParseAtIdentifier(arg)
	if err != nil {
		return nil, err
	}

	dir := identity.DefaultDirectory()
	return dir.Lookup(ctx, *id)
}

func (a *Auth) CreateInitialSession(ctx context.Context, username, appPassword string) (*comatproto.ServerCreateSession_Output, error) {
	resolved, err := ResolveIdent(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("invalid handle: %s", err)
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
		return nil, fmt.Errorf("invalid app password")
	}

	return atSession, nil
}

// Sessionish is an interface that provides access to the common fields of both types.
type Sessionish interface {
	GetAccessJwt() string
	GetActive() *bool
	GetDid() string
	GetDidDoc() *interface{}
	GetHandle() string
	GetRefreshJwt() string
	GetStatus() *string
}

// Create a wrapper type for ServerRefreshSession_Output
type RefreshSessionWrapper struct {
	*comatproto.ServerRefreshSession_Output
}

func (s *RefreshSessionWrapper) GetAccessJwt() string {
	return s.AccessJwt
}

func (s *RefreshSessionWrapper) GetActive() *bool {
	return s.Active
}

func (s *RefreshSessionWrapper) GetDid() string {
	return s.Did
}

func (s *RefreshSessionWrapper) GetDidDoc() *interface{} {
	return s.DidDoc
}

func (s *RefreshSessionWrapper) GetHandle() string {
	return s.Handle
}

func (s *RefreshSessionWrapper) GetRefreshJwt() string {
	return s.RefreshJwt
}

func (s *RefreshSessionWrapper) GetStatus() *string {
	return s.Status
}

// Create a wrapper type for ServerRefreshSession_Output
type CreateSessionWrapper struct {
	*comatproto.ServerCreateSession_Output
}

func (s *CreateSessionWrapper) GetAccessJwt() string {
	return s.AccessJwt
}

func (s *CreateSessionWrapper) GetActive() *bool {
	return s.Active
}

func (s *CreateSessionWrapper) GetDid() string {
	return s.Did
}

func (s *CreateSessionWrapper) GetDidDoc() *interface{} {
	return s.DidDoc
}

func (s *CreateSessionWrapper) GetHandle() string {
	return s.Handle
}

func (s *CreateSessionWrapper) GetRefreshJwt() string {
	return s.RefreshJwt
}

func (s *CreateSessionWrapper) GetStatus() *string {
	return s.Status
}

func (a *Auth) StoreSession(r *http.Request, w http.ResponseWriter, atSessionish Sessionish) error {
	var didDoc identity.DIDDocument

	bytes, _ := json.Marshal(atSessionish.GetDidDoc())
	err := json.Unmarshal(bytes, &didDoc)
	if err != nil {
		return fmt.Errorf("invalid did document for session")
	}

	identity := identity.ParseIdentity(&didDoc)
	pdsEndpoint := identity.PDSEndpoint()

	if pdsEndpoint == "" {
		return fmt.Errorf("no pds endpoint found")
	}

	clientSession, _ := a.Store.Get(r, appview.SESSION_NAME)
	clientSession.Values[appview.SESSION_HANDLE] = atSessionish.GetHandle()
	clientSession.Values[appview.SESSION_DID] = atSessionish.GetDid()
	clientSession.Values[appview.SESSION_PDS] = pdsEndpoint
	clientSession.Values[appview.SESSION_ACCESSJWT] = atSessionish.GetAccessJwt()
	clientSession.Values[appview.SESSION_REFRESHJWT] = atSessionish.GetRefreshJwt()
	clientSession.Values[appview.SESSION_EXPIRY] = time.Now().Add(time.Hour).Format(appview.TIME_LAYOUT)
	clientSession.Values[appview.SESSION_AUTHENTICATED] = true

	return clientSession.Save(r, w)
}
