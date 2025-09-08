package xrpc

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/types"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) RepoLog(w http.ResponseWriter, r *http.Request) {
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

	path := r.URL.Query().Get("path")
	cursor := r.URL.Query().Get("cursor")

	limit := 50 // default
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	ref, err := url.QueryUnescape(refParam)
	if err != nil {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("invalid ref parameter"),
		), http.StatusBadRequest)
		return
	}

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RefNotFound"),
			xrpcerr.WithMessage("repository or ref not found"),
		), http.StatusNotFound)
		return
	}

	offset := 0
	if cursor != "" {
		if o, err := strconv.Atoi(cursor); err == nil && o >= 0 {
			offset = o
		}
	}

	commits, err := gr.Commits(offset, limit)
	if err != nil {
		x.Logger.Error("fetching commits", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("PathNotFound"),
			xrpcerr.WithMessage("failed to read commit log"),
		), http.StatusNotFound)
		return
	}

	total, err := gr.TotalCommits()
	if err != nil {
		x.Logger.Error("fetching total commits", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InternalServerError"),
			xrpcerr.WithMessage("failed to fetch total commits"),
		), http.StatusNotFound)
		return
	}

	// Create response using existing types.RepoLogResponse
	response := types.RepoLogResponse{
		Commits: commits,
		Ref:     ref,
		Page:    (offset / limit) + 1,
		PerPage: limit,
		Total:   total,
	}

	if path != "" {
		response.Description = path
	}

	response.Log = true

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
