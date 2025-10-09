package xrpc

import (
	"encoding/json"
	"fmt"
	"net/http"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.org/core/api/tangled"
	"tangled.org/core/knotserver/git"
	"tangled.org/core/rbac"

	xrpcerr "tangled.org/core/xrpc/errors"
)

func (x *Xrpc) DeleteBranch(w http.ResponseWriter, r *http.Request) {
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

	var data tangled.RepoDeleteBranch_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
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
	resp, err := comatproto.RepoGetRecord(r.Context(), &xrpcc, "", tangled.RepoNSID, repoAt.Authority().String(), repoAt.RecordKey().String())
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	repo := resp.Value.Val.(*tangled.Repo)
	didPath, err := securejoin.SecureJoin(ident.DID.String(), repo.Name)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	if ok, err := x.Enforcer.IsPushAllowed(actorDid.String(), rbac.ThisServer, didPath); !ok || err != nil {
		l.Error("insufficent permissions", "did", actorDid.String(), "repo", didPath)
		writeError(w, xrpcerr.AccessControlError(actorDid.String()), http.StatusUnauthorized)
		return
	}

	path, _ := securejoin.SecureJoin(x.Config.Repo.ScanPath, didPath)
	gr, err := git.PlainOpen(path)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	err = gr.DeleteBranch(data.Branch)
	if err != nil {
		l.Error("deleting branch", "error", err.Error(), "branch", data.Branch)
		writeError(w, xrpcerr.GitError(err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
