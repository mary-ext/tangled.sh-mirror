package xrpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/hook"
	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/rbac"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) ForkRepo(w http.ResponseWriter, r *http.Request) {
	l := x.Logger.With("handler", "ForkRepo")
	fail := func(e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, http.StatusBadRequest)
	}

	actorDid, ok := r.Context().Value(ActorDid).(syntax.DID)
	if !ok {
		fail(xrpcerr.MissingActorDidError)
		return
	}

	isMember, err := x.Enforcer.IsKnotMember(actorDid.String(), rbac.ThisServer)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}
	if !isMember {
		fail(xrpcerr.AccessControlError(actorDid.String()))
		return
	}

	var data tangled.RepoFork_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	did := data.Did
	source := data.Source

	if did == "" || source == "" {
		fail(xrpcerr.GenericError(fmt.Errorf("did and source are required")))
		return
	}

	var name string
	if data.Name != nil && *data.Name != "" {
		name = *data.Name
	} else {
		name = filepath.Base(source)
	}

	relativeRepoPath := filepath.Join(did, name)
	repoPath, err := securejoin.SecureJoin(x.Config.Repo.ScanPath, relativeRepoPath)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	err = git.Fork(repoPath, source)
	if err != nil {
		l.Error("forking repo", "error", err.Error())
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}

	// add perms for this user to access the repo
	err = x.Enforcer.AddRepo(did, rbac.ThisServer, relativeRepoPath)
	if err != nil {
		l.Error("adding repo permissions", "error", err.Error())
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}

	hook.SetupRepo(
		hook.Config(
			hook.WithScanPath(x.Config.Repo.ScanPath),
			hook.WithInternalApi(x.Config.Server.InternalListenAddr),
		),
		repoPath,
	)

	w.WriteHeader(http.StatusOK)
}
