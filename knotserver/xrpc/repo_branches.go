package xrpc

import (
	"encoding/json"
	"net/http"
	"strconv"

	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/types"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) RepoBranches(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	cursor := r.URL.Query().Get("cursor")

	// limit := 50 // default
	// if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
	// 	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
	// 		limit = l
	// 	}
	// }

	limit := 500

	gr, err := git.PlainOpen(repoPath)
	if err != nil {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RepoNotFound"),
			xrpcerr.WithMessage("repository not found"),
		), http.StatusNotFound)
		return
	}

	branches, _ := gr.Branches()

	offset := 0
	if cursor != "" {
		if o, err := strconv.Atoi(cursor); err == nil && o >= 0 && o < len(branches) {
			offset = o
		}
	}

	end := offset + limit
	if end > len(branches) {
		end = len(branches)
	}

	paginatedBranches := branches[offset:end]

	// Create response using existing types.RepoBranchesResponse
	response := types.RepoBranchesResponse{
		Branches: paginatedBranches,
	}

	// Write JSON response directly
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		x.Logger.Error("failed to encode response", "error", err)
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InternalServerError"),
			xrpcerr.WithMessage("failed to encode response"),
		), http.StatusInternalServerError)
		return
	}
}
