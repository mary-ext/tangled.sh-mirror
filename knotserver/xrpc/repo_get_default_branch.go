package xrpc

import (
	"encoding/json"
	"net/http"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/git"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) RepoGetDefaultBranch(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	gr, err := git.PlainOpen(repoPath)

	branch, err := gr.FindMainBranch()
	if err != nil {
		x.Logger.Error("getting default branch", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("failed to get default branch"),
		), http.StatusInternalServerError)
		return
	}

	response := tangled.RepoGetDefaultBranch_Output{
		Name: branch,
		Hash: "",
		When: time.UnixMicro(0).Format(time.RFC3339),
	}

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
