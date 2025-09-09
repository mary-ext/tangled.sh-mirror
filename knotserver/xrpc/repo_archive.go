package xrpc

import (
	"compress/gzip"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"tangled.sh/tangled.sh/core/knotserver/git"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) RepoArchive(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	ref := r.URL.Query().Get("ref")
	// ref can be empty (git.Open handles this)

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "tar.gz" // default
	}

	prefix := r.URL.Query().Get("prefix")

	if format != "tar.gz" {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("only tar.gz format is supported"),
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

	repoParts := strings.Split(repo, "/")
	repoName := repoParts[len(repoParts)-1]

	safeRefFilename := strings.ReplaceAll(plumbing.ReferenceName(ref).Short(), "/", "-")

	var archivePrefix string
	if prefix != "" {
		archivePrefix = prefix
	} else {
		archivePrefix = fmt.Sprintf("%s-%s", repoName, safeRefFilename)
	}

	filename := fmt.Sprintf("%s-%s.tar.gz", repoName, safeRefFilename)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Type", "application/gzip")

	gw := gzip.NewWriter(w)
	defer gw.Close()

	err = gr.WriteTar(gw, archivePrefix)
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with logging the error
		x.Logger.Error("writing tar file", "error", err.Error())
		return
	}

	err = gw.Flush()
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with logging the error
		x.Logger.Error("flushing", "error", err.Error())
		return
	}
}
