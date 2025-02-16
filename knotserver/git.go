package knotserver

import (
	"compress/gzip"
	"io"
	"net/http"
	"path/filepath"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/sotangled/tangled/knotserver/git/service"
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
		http.Error(w, err.Error(), 500)
		d.l.Error("git: failed to execute git-upload-pack (info/refs)", "handler", "InfoRefs", "error", err)
		return
	}
}

func (d *Handle) UploadPack(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")
	name := chi.URLParam(r, "name")
	repo, _ := securejoin.SecureJoin(d.c.Repo.ScanPath, filepath.Join(did, name))

	w.Header().Set("content-type", "application/x-git-upload-pack-result")
	w.Header().Set("Connection", "Keep-Alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	cmd := service.ServiceCommand{
		Dir:    repo,
		Stdout: w,
	}

	var reader io.ReadCloser
	reader = r.Body

	if r.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			d.l.Error("git: failed to create gzip reader", "handler", "UploadPack", "error", err)
			return
		}
		defer reader.Close()
	}

	cmd.Stdin = reader
	if err := cmd.UploadPack(); err != nil {
		http.Error(w, err.Error(), 500)
		d.l.Error("git: failed to execute git-upload-pack", "handler", "UploadPack", "error", err)
		return
	}
}
