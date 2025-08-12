package xrpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/types"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) ForkStatus(w http.ResponseWriter, r *http.Request) {
	l := x.Logger.With("handler", "ForkStatus")
	fail := func(e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, http.StatusBadRequest)
	}

	actorDid, ok := r.Context().Value(ActorDid).(syntax.DID)
	if !ok {
		fail(xrpcerr.MissingActorDidError)
		return
	}

	var data tangled.RepoForkStatus_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	did := data.Did
	source := data.Source
	branch := data.Branch
	hiddenRef := data.HiddenRef

	if did == "" || source == "" || branch == "" || hiddenRef == "" {
		fail(xrpcerr.GenericError(fmt.Errorf("did, source, branch, and hiddenRef are required")))
		return
	}

	var name string
	if data.Name != "" {
		name = data.Name
	} else {
		name = filepath.Base(source)
	}

	relativeRepoPath := filepath.Join(did, name)

	if ok, err := x.Enforcer.IsPushAllowed(actorDid.String(), rbac.ThisServer, relativeRepoPath); !ok || err != nil {
		l.Error("insufficient permissions", "did", actorDid.String(), "repo", relativeRepoPath)
		writeError(w, xrpcerr.AccessControlError(actorDid.String()), http.StatusUnauthorized)
		return
	}

	repoPath, err := securejoin.SecureJoin(x.Config.Repo.ScanPath, relativeRepoPath)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	gr, err := git.PlainOpen(repoPath)
	if err != nil {
		fail(xrpcerr.GenericError(fmt.Errorf("failed to open repository: %w", err)))
		return
	}

	forkCommit, err := gr.ResolveRevision(branch)
	if err != nil {
		l.Error("error resolving ref revision", "msg", err.Error())
		fail(xrpcerr.GenericError(fmt.Errorf("error resolving revision %s: %w", branch, err)))
		return
	}

	sourceCommit, err := gr.ResolveRevision(hiddenRef)
	if err != nil {
		l.Error("error resolving hidden ref revision", "msg", err.Error())
		fail(xrpcerr.GenericError(fmt.Errorf("error resolving revision %s: %w", hiddenRef, err)))
		return
	}

	status := types.UpToDate
	if forkCommit.Hash.String() != sourceCommit.Hash.String() {
		isAncestor, err := forkCommit.IsAncestor(sourceCommit)
		if err != nil {
			l.Error("error checking ancestor relationship", "error", err.Error())
			fail(xrpcerr.GenericError(fmt.Errorf("error resolving whether %s is ancestor of %s: %w", branch, hiddenRef, err)))
			return
		}

		if isAncestor {
			status = types.FastForwardable
		} else {
			status = types.Conflict
		}
	}

	response := tangled.RepoForkStatus_Output{
		Status: int64(status),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
