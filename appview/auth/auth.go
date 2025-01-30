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
	"github.com/whyrusleeping/go-did"
)

type Auth struct {
	store sessions.Store
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
	store := sessions.NewCookieStore([]byte("TODO_CHANGE_ME"))
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

func (a *Auth) StoreSession(r *http.Request, w http.ResponseWriter, atSession *comatproto.ServerCreateSession_Output) error {
	didDoc, ok := (*atSession.DidDoc).(did.Document)
	if !ok {
		return fmt.Errorf("invalid did document for session")
	}

	pdsEndpoint := getPdsEndpoint(&didDoc)

	if pdsEndpoint == "" {
		return fmt.Errorf("no pds endpoint found")
	}

	clientSession, _ := a.store.Get(r, "appview-session")
	clientSession.Values["handle"] = atSession.Handle
	clientSession.Values["did"] = atSession.Did
	clientSession.Values["pds"] = pdsEndpoint
	clientSession.Values["accessJwt"] = atSession.AccessJwt
	clientSession.Values["refreshJwt"] = atSession.RefreshJwt
	clientSession.Values["expiry"] = time.Now().Add(time.Hour).String()
	clientSession.Values["authenticated"] = true

	return clientSession.Save(r, w)
}

func getPdsEndpoint(didDoc *did.Document) string {
	fullId := didDoc.ID.String()
	id := "#atproto_pds"
	type_ := "AtprotoPersonalDataServer"

	for _, service := range didDoc.Service {
		serviceId := service.ID.String()
		if serviceId == id || serviceId == fullId+id || service.Type == type_ {
			return service.ServiceEndpoint
		}
	}

	return ""
}
