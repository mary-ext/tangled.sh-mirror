package xrpc

// import (
// 	"encoding/json"
// 	"fmt"
// 	"net/http"
// 	"os"
// 	"path/filepath"
//
// 	"github.com/bluesky-social/indigo/atproto/syntax"
// 	securejoin "github.com/cyphar/filepath-securejoin"
// 	"tangled.sh/tangled.sh/core/api/tangled"
// 	"tangled.sh/tangled.sh/core/rbac"
// 	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
// )

// func (x *Xrpc) DeleteRepo(w http.ResponseWriter, r *http.Request) {
// 	l := x.Logger.With("handler", "DeleteRepo")
// 	fail := func(e xrpcerr.XrpcError) {
// 		l.Error("failed", "kind", e.Tag, "error", e.Message)
// 		writeError(w, e, http.StatusBadRequest)
// 	}
//
// 	actorDid, ok := r.Context().Value(ActorDid).(syntax.DID)
// 	if !ok {
// 		fail(xrpcerr.MissingActorDidError)
// 		return
// 	}
//
// 	isMember, err := x.Enforcer.IsRepoDeleteAllowed(actorDid.String(), rbac.ThisServer)
// 	if err != nil {
// 		fail(xrpcerr.GenericError(err))
// 		return
// 	}
// 	if !isMember {
// 		fail(xrpcerr.AccessControlError(actorDid.String()))
// 		return
// 	}
//
// 	var data tangled.RepoDelete_Input
// 	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
// 		fail(xrpcerr.GenericError(err))
// 		return
// 	}
//
// 	did := data.Did
// 	name := data.Name
//
// 	if did == "" || name == "" {
// 		fail(xrpcerr.GenericError(fmt.Errorf("did and name are required")))
// 		return
// 	}
//
// 	relativeRepoPath := filepath.Join(did, name)
// 	if ok, err := x.Enforcer.IsSettingsAllowed(actorDid.String(), rbac.ThisServer, relativeRepoPath); !ok || err != nil {
// 		l.Error("insufficient permissions", "did", actorDid.String(), "repo", relativeRepoPath)
// 		writeError(w, xrpcerr.AccessControlError(actorDid.String()), http.StatusUnauthorized)
// 		return
// 	}
//
// 	repoPath, err := securejoin.SecureJoin(x.Config.Repo.ScanPath, relativeRepoPath)
// 	if err != nil {
// 		fail(xrpcerr.GenericError(err))
// 		return
// 	}
//
// 	err = os.RemoveAll(repoPath)
// 	if err != nil {
// 		l.Error("deleting repo", "error", err.Error())
// 		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
// 		return
// 	}
//
// 	err = x.Enforcer.RemoveRepo(did, rbac.ThisServer, relativeRepoPath)
// 	if err != nil {
// 		l.Error("failed to delete repo from enforcer", "error", err.Error())
// 		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
// 		return
// 	}
//
// 	w.WriteHeader(http.StatusOK)
// }
