package xrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/types"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) Merge(w http.ResponseWriter, r *http.Request) {
	l := x.Logger.With("handler", "Merge")
	fail := func(e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, http.StatusBadRequest)
	}

	actorDid, ok := r.Context().Value(ActorDid).(syntax.DID)
	if !ok {
		fail(xrpcerr.MissingActorDidError)
		return
	}

	var data tangled.RepoMerge_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	did := data.Did
	name := data.Name

	if did == "" || name == "" {
		fail(xrpcerr.GenericError(fmt.Errorf("did and name are required")))
		return
	}

	relativeRepoPath, err := securejoin.SecureJoin(did, name)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

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

	gr, err := git.Open(repoPath, data.Branch)
	if err != nil {
		fail(xrpcerr.GenericError(fmt.Errorf("failed to open repository: %w", err)))
		return
	}

	mo := &git.MergeOptions{}
	if data.AuthorName != nil {
		mo.AuthorName = *data.AuthorName
	}
	if data.AuthorEmail != nil {
		mo.AuthorEmail = *data.AuthorEmail
	}
	if data.CommitBody != nil {
		mo.CommitBody = *data.CommitBody
	}
	if data.CommitMessage != nil {
		mo.CommitMessage = *data.CommitMessage
	}

	mo.FormatPatch = patchutil.IsFormatPatch(data.Patch)

	err = gr.MergeWithOptions([]byte(data.Patch), data.Branch, mo)
	if err != nil {
		var mergeErr *git.ErrMerge
		if errors.As(err, &mergeErr) {
			conflicts := make([]types.ConflictInfo, len(mergeErr.Conflicts))
			for i, conflict := range mergeErr.Conflicts {
				conflicts[i] = types.ConflictInfo{
					Filename: conflict.Filename,
					Reason:   conflict.Reason,
				}
			}

			conflictErr := xrpcerr.NewXrpcError(
				xrpcerr.WithTag("MergeConflict"),
				xrpcerr.WithMessage(fmt.Sprintf("Merge failed due to conflicts: %s", mergeErr.Message)),
			)
			writeError(w, conflictErr, http.StatusConflict)
			return
		} else {
			l.Error("failed to merge", "error", err.Error())
			writeError(w, xrpcerr.GitError(err), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}
