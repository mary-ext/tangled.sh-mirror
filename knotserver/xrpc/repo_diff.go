package xrpc

import (
	"net/http"

	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/types"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) RepoDiff(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	ref := r.URL.Query().Get("ref")
	// ref can be empty (git.Open handles this)

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		writeError(w, xrpcerr.RefNotFoundError, http.StatusNotFound)
		return
	}

	diff, err := gr.Diff()
	if err != nil {
		x.Logger.Error("getting diff", "error", err.Error())
		writeError(w, xrpcerr.RefNotFoundError, http.StatusInternalServerError)
		return
	}

	response := types.RepoCommitResponse{
		Ref:  ref,
		Diff: diff,
	}

	writeJson(w, response)
}
