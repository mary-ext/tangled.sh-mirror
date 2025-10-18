package knotserver

import (
	"compress/gzip"
	"fmt"
	"net/http"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"tangled.org/core/knotserver/git"
)

func (h *Knot) Archive(w http.ResponseWriter, r *http.Request) {
	var (
		did  = chi.URLParam(r, "did")
		name = chi.URLParam(r, "name")
		ref  = chi.URLParam(r, "ref")
	)
	repo, err := securejoin.SecureJoin(did, name)
	if err != nil {
		gitError(w, "repository not found", http.StatusNotFound)
		h.l.Error("git: failed to secure join repo path", "handler", "InfoRefs", "error", err)
		return
	}

	repoPath, err := securejoin.SecureJoin(h.c.Repo.ScanPath, repo)
	if err != nil {
		gitError(w, "repository not found", http.StatusNotFound)
		h.l.Error("git: failed to secure join repo path", "handler", "InfoRefs", "error", err)
		return
	}

	gr, err := git.Open(repoPath, ref)

	immutableLink := fmt.Sprintf(
		"https://%s/%s/%s/archive/%s",
		h.c.Server.Hostname,
		did,
		name,
		gr.Hash(),
	)

	safeRefFilename := strings.ReplaceAll(plumbing.ReferenceName(ref).Short(), "/", "-")
	filename := fmt.Sprintf("%s-%s.tar.gz", name, safeRefFilename)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"immutable\"", immutableLink))

	gw := gzip.NewWriter(w)
	defer gw.Close()

	err = gr.WriteTar(gw, "")
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with logging the error
		h.l.Error("writing tar file", "error", err)
		return
	}

	err = gw.Flush()
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with logging the error
		h.l.Error("flushing", "error", err.Error())
		return
	}
}
