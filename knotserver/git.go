package knotserver

import (
	"compress/gzip"
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
	repo, _ := securejoin.SecureJoin(d.c.Repo.ScanPath, filepath.Join(did, name))

	w.Header().Set("content-type", "application/x-git-upload-pack-advertisement")
	w.WriteHeader(http.StatusOK)

	cmd := service.ServiceCommand{
		Dir:    repo,
		Stdout: w,
	}

	if err := cmd.InfoRefs(); err != nil {
		writeError(w, err.Error(), 500)
		d.l.Error("git: failed to execute git-upload-pack (info/refs)", "handler", "InfoRefs", "error", err)
		return
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
