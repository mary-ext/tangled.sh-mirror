package xrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/git"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) MergeCheck(w http.ResponseWriter, r *http.Request) {
	l := x.Logger.With("handler", "MergeCheck")
	fail := func(e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, http.StatusBadRequest)
	}

	var data tangled.RepoMergeCheck_Input
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

	err = gr.MergeCheck([]byte(data.Patch), data.Branch)

	response := tangled.RepoMergeCheck_Output{
		Is_conflicted: false,
	}

	if err != nil {
		var mergeErr *git.ErrMerge
		if errors.As(err, &mergeErr) {
			response.Is_conflicted = true

			conflicts := make([]*tangled.RepoMergeCheck_ConflictInfo, len(mergeErr.Conflicts))
			for i, conflict := range mergeErr.Conflicts {
				conflicts[i] = &tangled.RepoMergeCheck_ConflictInfo{
					Filename: conflict.Filename,
					Reason:   conflict.Reason,
				}
			}
			response.Conflicts = conflicts

			if mergeErr.Message != "" {
				response.Message = &mergeErr.Message
			}
		} else {
			response.Is_conflicted = true
			errMsg := err.Error()
			response.Error = &errMsg
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
