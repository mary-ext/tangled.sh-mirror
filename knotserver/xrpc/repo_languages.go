package xrpc

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/git"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) RepoLanguages(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	ref := r.URL.Query().Get("ref")

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		x.Logger.Error("opening repo", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RefNotFound"),
			xrpcerr.WithMessage("repository or ref not found"),
		), http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	sizes, err := gr.AnalyzeLanguages(ctx)
	if err != nil {
		x.Logger.Error("failed to analyze languages", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("failed to analyze repository languages"),
		), http.StatusNoContent)
		return
	}

	var apiLanguages []*tangled.RepoLanguages_Language
	var totalSize int64

	for _, size := range sizes {
		totalSize += size
	}

	for name, size := range sizes {
		percentagef64 := float64(size) / float64(totalSize) * 100
		percentage := math.Round(percentagef64)

		lang := &tangled.RepoLanguages_Language{
			Name:       name,
			Size:       size,
			Percentage: int64(percentage),
		}

		apiLanguages = append(apiLanguages, lang)
	}

	response := tangled.RepoLanguages_Output{
		Ref:       ref,
		Languages: apiLanguages,
	}

	if totalSize > 0 {
		response.TotalSize = &totalSize
		totalFiles := int64(len(sizes))
		response.TotalFiles = &totalFiles
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
