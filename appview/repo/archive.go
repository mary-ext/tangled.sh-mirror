package repo

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"tangled.org/core/api/tangled"
	xrpcclient "tangled.org/core/appview/xrpcclient"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func (rp *Repo) DownloadArchive(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "DownloadArchive")
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}
	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}
	didSlashRepo := f.DidSlashRepo()
	archiveBytes, err := tangled.RepoArchive(r.Context(), xrpcc, "tar.gz", "", ref, didSlashRepo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.archive", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}
	// Set headers for file download, just pass along whatever the knot specifies
	safeRefFilename := strings.ReplaceAll(plumbing.ReferenceName(ref).Short(), "/", "-")
	filename := fmt.Sprintf("%s-%s.tar.gz", f.Name, safeRefFilename)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(archiveBytes)))
	// Write the archive data directly
	w.Write(archiveBytes)
}
