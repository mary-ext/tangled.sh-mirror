package xrpc

import (
	"net/http"
	"strconv"

	"tangled.org/core/knotserver/git"
	"tangled.org/core/types"
	xrpcerr "tangled.org/core/xrpc/errors"
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
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNoContent)
		return
	}

	branches, _ := gr.Branches()

	offset := 0
	if cursor != "" {
		if o, err := strconv.Atoi(cursor); err == nil && o >= 0 && o < len(branches) {
			offset = o
		}
	}

	end := min(offset+limit, len(branches))

	paginatedBranches := branches[offset:end]

	// Create response using existing types.RepoBranchesResponse
	response := types.RepoBranchesResponse{
		Branches: paginatedBranches,
	}

	writeJson(w, response)
}
