package xrpc

import (
	"encoding/json"
	"net/http"
	"net/url"

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

	refParam := r.URL.Query().Get("ref")
	if refParam == "" {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("missing ref parameter"),
		), http.StatusBadRequest)
		return
	}

	ref, _ := url.QueryUnescape(refParam)

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RefNotFound"),
			xrpcerr.WithMessage("repository or ref not found"),
		), http.StatusNotFound)
		return
	}

	diff, err := gr.Diff()
	if err != nil {
		x.Logger.Error("getting diff", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RefNotFound"),
			xrpcerr.WithMessage("failed to generate diff"),
		), http.StatusInternalServerError)
		return
	}

	resp := types.RepoCommitResponse{
		Ref:  ref,
		Diff: diff,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		x.Logger.Error("failed to encode response", "error", err)
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InternalServerError"),
			xrpcerr.WithMessage("failed to encode response"),
		), http.StatusInternalServerError)
		return
	}
}
