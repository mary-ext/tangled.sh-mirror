package xrpc

import (
	"encoding/json"
	"fmt"
	"net/http"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/rbac"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) HiddenRef(w http.ResponseWriter, r *http.Request) {
	l := x.Logger.With("handler", "HiddenRef")
	fail := func(e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, http.StatusBadRequest)
	}

	actorDid, ok := r.Context().Value(ActorDid).(syntax.DID)
	if !ok {
		fail(xrpcerr.MissingActorDidError)
		return
	}

	var data tangled.RepoHiddenRef_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	forkRef := data.ForkRef
	remoteRef := data.RemoteRef
	repoAtUri := data.Repo

	if forkRef == "" || remoteRef == "" || repoAtUri == "" {
		fail(xrpcerr.GenericError(fmt.Errorf("forkRef, remoteRef, and repo are required")))
		return
	}

	repoAt, err := syntax.ParseATURI(repoAtUri)
	if err != nil {
		fail(xrpcerr.InvalidRepoError(repoAtUri))
		return
	}

	ident, err := x.Resolver.ResolveIdent(r.Context(), repoAt.Authority().String())
	if err != nil || ident.Handle.IsInvalidHandle() {
		fail(xrpcerr.GenericError(fmt.Errorf("failed to resolve handle: %w", err)))
		return
	}

	xrpcc := xrpc.Client{Host: ident.PDSEndpoint()}
	resp, err := comatproto.RepoGetRecord(r.Context(), &xrpcc, "", tangled.RepoNSID, repoAt.Authority().String(), repoAt.RecordKey().String())
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	repo := resp.Value.Val.(*tangled.Repo)
	didPath, err := securejoin.SecureJoin(actorDid.String(), repo.Name)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	if ok, err := x.Enforcer.IsPushAllowed(actorDid.String(), rbac.ThisServer, didPath); !ok || err != nil {
		l.Error("insufficient permissions", "did", actorDid.String(), "repo", didPath)
		writeError(w, xrpcerr.AccessControlError(actorDid.String()), http.StatusUnauthorized)
		return
	}

	repoPath, err := securejoin.SecureJoin(x.Config.Repo.ScanPath, didPath)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	gr, err := git.PlainOpen(repoPath)
	if err != nil {
		fail(xrpcerr.GenericError(fmt.Errorf("failed to open repository: %w", err)))
		return
	}

	err = gr.TrackHiddenRemoteRef(forkRef, remoteRef)
	if err != nil {
		l.Error("error tracking hidden remote ref", "error", err.Error())
		writeError(w, xrpcerr.GitError(err), http.StatusInternalServerError)
		return
	}

	response := tangled.RepoHiddenRef_Output{
		Success: true,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
