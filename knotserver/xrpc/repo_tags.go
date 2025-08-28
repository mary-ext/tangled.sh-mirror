package xrpc

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/types"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) RepoTags(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	cursor := r.URL.Query().Get("cursor")

	limit := 50 // default
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	gr, err := git.Open(repoPath, "")
	if err != nil {
		x.Logger.Error("failed to open", "error", err)
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RepoNotFound"),
			xrpcerr.WithMessage("repository not found"),
		), http.StatusNotFound)
		return
	}

	tags, err := gr.Tags()
	if err != nil {
		x.Logger.Warn("getting tags", "error", err.Error())
		tags = []object.Tag{}
	}

	rtags := []*types.TagReference{}
	for _, tag := range tags {
		var target *object.Tag
		if tag.Target != plumbing.ZeroHash {
			target = &tag
		}
		tr := types.TagReference{
			Tag: target,
		}

		tr.Reference = types.Reference{
			Name: tag.Name,
			Hash: tag.Hash.String(),
		}

		if tag.Message != "" {
			tr.Message = tag.Message
		}

		rtags = append(rtags, &tr)
	}

	// apply pagination manually
	offset := 0
	if cursor != "" {
		if o, err := strconv.Atoi(cursor); err == nil && o >= 0 && o < len(rtags) {
			offset = o
		}
	}

	// calculate end index
	end := min(offset+limit, len(rtags))

	paginatedTags := rtags[offset:end]

	// Create response using existing types.RepoTagsResponse
	response := types.RepoTagsResponse{
		Tags: paginatedTags,
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
