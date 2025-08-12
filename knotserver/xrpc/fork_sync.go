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
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) ForkSync(w http.ResponseWriter, r *http.Request) {
	l := x.Logger.With("handler", "ForkSync")
	fail := func(e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, http.StatusBadRequest)
	}

	actorDid, ok := r.Context().Value(ActorDid).(syntax.DID)
	if !ok {
		fail(xrpcerr.MissingActorDidError)
		return
	}

	var data tangled.RepoForkSync_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	did := data.Did
	name := data.Name
	branch := data.Branch

	if did == "" || name == "" {
		fail(xrpcerr.GenericError(fmt.Errorf("did, name are required")))
		return
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

	gr, err := git.Open(repoPath, branch)
	if err != nil {
		fail(xrpcerr.GenericError(fmt.Errorf("failed to open repository: %w", err)))
		return
	}

	err = gr.Sync()
	if err != nil {
		l.Error("error syncing repo fork", "error", err.Error())
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
