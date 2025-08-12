package xrpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/secrets"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) AddSecret(w http.ResponseWriter, r *http.Request) {
	l := x.Logger
	fail := func(e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, http.StatusBadRequest)
	}

	actorDid, ok := r.Context().Value(ActorDid).(syntax.DID)
	if !ok {
		fail(xrpcerr.MissingActorDidError)
		return
	}

	var data tangled.RepoAddSecret_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	if err := secrets.ValidateKey(data.Key); err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	// unfortunately we have to resolve repo-at here
	repoAt, err := syntax.ParseATURI(data.Repo)
	if err != nil {
		fail(xrpcerr.InvalidRepoError(data.Repo))
		return
	}

	// resolve this aturi to extract the repo record
	ident, err := x.Resolver.ResolveIdent(r.Context(), repoAt.Authority().String())
	if err != nil || ident.Handle.IsInvalidHandle() {
		fail(xrpcerr.GenericError(fmt.Errorf("failed to resolve handle: %w", err)))
		return
	}

	xrpcc := xrpc.Client{Host: ident.PDSEndpoint()}
	resp, err := atproto.RepoGetRecord(r.Context(), &xrpcc, "", tangled.RepoNSID, repoAt.Authority().String(), repoAt.RecordKey().String())
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	repo := resp.Value.Val.(*tangled.Repo)
	didPath, err := securejoin.SecureJoin(repo.Owner, repo.Name)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	if ok, err := x.Enforcer.IsSettingsAllowed(actorDid.String(), rbac.ThisServer, didPath); !ok || err != nil {
		l.Error("insufficent permissions", "did", actorDid.String())
		writeError(w, xrpcerr.AccessControlError(actorDid.String()), http.StatusUnauthorized)
		return
	}

	secret := secrets.UnlockedSecret{
		Repo:      secrets.DidSlashRepo(didPath),
		Key:       data.Key,
		Value:     data.Value,
		CreatedAt: time.Now(),
		CreatedBy: actorDid,
	}
	err = x.Vault.AddSecret(r.Context(), secret)
	if err != nil {
		l.Error("failed to add secret to vault", "did", actorDid.String(), "err", err)
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
