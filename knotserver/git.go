package knotserver

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/knotserver/git/service"
)

func (d *Handle) InfoRefs(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")
	name := chi.URLParam(r, "name")
	repoName, err := securejoin.SecureJoin(did, name)
	if err != nil {
		gitError(w, "repository not found", http.StatusNotFound)
		d.l.Error("git: failed to secure join repo path", "handler", "InfoRefs", "error", err)
		return
	}

	repoPath, err := securejoin.SecureJoin(d.c.Repo.ScanPath, repoName)
	if err != nil {
		gitError(w, "repository not found", http.StatusNotFound)
		d.l.Error("git: failed to secure join repo path", "handler", "InfoRefs", "error", err)
		return
	}

	cmd := service.ServiceCommand{
		Dir:    repoPath,
		Stdout: w,
	}

	serviceName := r.URL.Query().Get("service")
	switch serviceName {
	case "git-upload-pack":
		w.Header().Set("content-type", "application/x-git-upload-pack-advertisement")
		w.WriteHeader(http.StatusOK)

		if err := cmd.InfoRefs(); err != nil {
			gitError(w, err.Error(), http.StatusInternalServerError)
			d.l.Error("git: process failed", "handler", "InfoRefs", "service", serviceName, "error", err)
			return
		}
	case "git-receive-pack":
		d.RejectPush(w, r, name)
	default:
		gitError(w, fmt.Sprintf("service unsupported: '%s'", serviceName), http.StatusForbidden)
	}
}

func (d *Handle) UploadPack(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")
	name := chi.URLParam(r, "name")
	repo, err := securejoin.SecureJoin(d.c.Repo.ScanPath, filepath.Join(did, name))
	if err != nil {
		writeError(w, err.Error(), 500)
		d.l.Error("git: failed to secure join repo path", "handler", "UploadPack", "error", err)
		return
	}

	var bodyReader io.ReadCloser = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			writeError(w, err.Error(), 500)
			d.l.Error("git: failed to create gzip reader", "handler", "UploadPack", "error", err)
			return
		}
		defer gzipReader.Close()
		bodyReader = gzipReader
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Connection", "Keep-Alive")

	d.l.Info("git: executing git-upload-pack", "handler", "UploadPack", "repo", repo)

	cmd := service.ServiceCommand{
		Dir:    repo,
		Stdout: w,
		Stdin:  bodyReader,
	}

	w.WriteHeader(http.StatusOK)

	if err := cmd.UploadPack(); err != nil {
		d.l.Error("git: failed to execute git-upload-pack", "handler", "UploadPack", "error", err)
		return
	}
}

func (d *Handle) ReceivePack(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")
	name := chi.URLParam(r, "name")
	_, err := securejoin.SecureJoin(d.c.Repo.ScanPath, filepath.Join(did, name))
	if err != nil {
		gitError(w, err.Error(), http.StatusForbidden)
		d.l.Error("git: failed to secure join repo path", "handler", "ReceivePack", "error", err)
		return
	}

	d.RejectPush(w, r, name)
}

func (d *Handle) RejectPush(w http.ResponseWriter, r *http.Request, unqualifiedRepoName string) {
	// A text/plain response will cause git to print each line of the body
	// prefixed with "remote: ".
	w.Header().Set("content-type", "text/plain; charset=UTF-8")
	w.WriteHeader(http.StatusForbidden)

	fmt.Fprintf(w, "Welcome to Tangled.sh!\n\nPushes are currently only supported over SSH.")
	fmt.Fprintf(w, "\n\n")
}

func gitError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("content-type", "text/plain; charset=UTF-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "%s\n", msg)
}
