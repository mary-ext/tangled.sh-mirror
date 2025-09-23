package xrpc

import (
	"net/http"
	"strconv"

	"tangled.org/core/knotserver/git"
	"tangled.org/core/types"
	xrpcerr "tangled.org/core/xrpc/errors"
)

func (x *Xrpc) RepoLog(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	ref := r.URL.Query().Get("ref")

	path := r.URL.Query().Get("path")
	cursor := r.URL.Query().Get("cursor")

	limit := 50 // default
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		writeError(w, xrpcerr.RefNotFoundError, http.StatusNotFound)
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

	writeJson(w, response)
}
