package repo

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pages/markup"
	xrpcclient "tangled.org/core/appview/xrpcclient"

	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
)

func (rp *Repo) Blob(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoBlob")
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)
	filePath := chi.URLParam(r, "*")
	filePath, _ = url.PathUnescape(filePath)
	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}
	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Repo.Name)
	resp, err := tangled.RepoBlob(r.Context(), xrpcc, filePath, false, ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.blob", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}
	// Use XRPC response directly instead of converting to internal types
	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.Name, fmt.Sprintf("/%s/tree/%s", f.OwnerSlashRepo(), url.PathEscape(ref))})
	if filePath != "" {
		for idx, elem := range strings.Split(filePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], url.PathEscape(elem))})
		}
	}
	showRendered := false
	renderToggle := false
	if markup.GetFormat(resp.Path) == markup.FormatMarkdown {
		renderToggle = true
		showRendered = r.URL.Query().Get("code") != "true"
	}
	var unsupported bool
	var isImage bool
	var isVideo bool
	var contentSrc string
	if resp.IsBinary != nil && *resp.IsBinary {
		ext := strings.ToLower(filepath.Ext(resp.Path))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp":
			isImage = true
		case ".mp4", ".webm", ".ogg", ".mov", ".avi":
			isVideo = true
		default:
			unsupported = true
		}
		// fetch the raw binary content using sh.tangled.repo.blob xrpc
		repoName := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
		baseURL := &url.URL{
			Scheme: scheme,
			Host:   f.Knot,
			Path:   "/xrpc/sh.tangled.repo.blob",
		}
		query := baseURL.Query()
		query.Set("repo", repoName)
		query.Set("ref", ref)
		query.Set("path", filePath)
		query.Set("raw", "true")
		baseURL.RawQuery = query.Encode()
		blobURL := baseURL.String()
		contentSrc = blobURL
		if !rp.config.Core.Dev {
			contentSrc = markup.GenerateCamoURL(rp.config.Camo.Host, rp.config.Camo.SharedSecret, blobURL)
		}
	}
	lines := 0
	if resp.IsBinary == nil || !*resp.IsBinary {
		lines = strings.Count(resp.Content, "\n") + 1
	}
	var sizeHint uint64
	if resp.Size != nil {
		sizeHint = uint64(*resp.Size)
	} else {
		sizeHint = uint64(len(resp.Content))
	}
	user := rp.oauth.GetUser(r)
	// Determine if content is binary (dereference pointer)
	isBinary := false
	if resp.IsBinary != nil {
		isBinary = *resp.IsBinary
	}
	rp.pages.RepoBlob(w, pages.RepoBlobParams{
		LoggedInUser:    user,
		RepoInfo:        f.RepoInfo(user),
		BreadCrumbs:     breadcrumbs,
		ShowRendered:    showRendered,
		RenderToggle:    renderToggle,
		Unsupported:     unsupported,
		IsImage:         isImage,
		IsVideo:         isVideo,
		ContentSrc:      contentSrc,
		RepoBlob_Output: resp,
		Contents:        resp.Content,
		Lines:           lines,
		SizeHint:        sizeHint,
		IsBinary:        isBinary,
	})
}

func (rp *Repo) RepoBlobRaw(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoBlobRaw")
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)
	filePath := chi.URLParam(r, "*")
	filePath, _ = url.PathUnescape(filePath)
	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Repo.Name)
	baseURL := &url.URL{
		Scheme: scheme,
		Host:   f.Knot,
		Path:   "/xrpc/sh.tangled.repo.blob",
	}
	query := baseURL.Query()
	query.Set("repo", repo)
	query.Set("ref", ref)
	query.Set("path", filePath)
	query.Set("raw", "true")
	baseURL.RawQuery = query.Encode()
	blobURL := baseURL.String()
	req, err := http.NewRequest("GET", blobURL, nil)
	if err != nil {
		l.Error("failed to create request", "err", err)
		return
	}
	// forward the If-None-Match header
	if clientETag := r.Header.Get("If-None-Match"); clientETag != "" {
		req.Header.Set("If-None-Match", clientETag)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		l.Error("failed to reach knotserver", "err", err)
		rp.pages.Error503(w)
		return
	}
	defer resp.Body.Close()
	// forward 304 not modified
	if resp.StatusCode == http.StatusNotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if resp.StatusCode != http.StatusOK {
		l.Error("knotserver returned non-OK status for raw blob", "url", blobURL, "statuscode", resp.StatusCode)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}
	contentType := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.Error("error reading response body from knotserver", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if strings.HasPrefix(contentType, "text/") || isTextualMimeType(contentType) {
		// serve all textual content as text/plain
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(body)
	} else if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") {
		// serve images and videos with their original content type
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	} else {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		w.Write([]byte("unsupported content type"))
		return
	}
}

func isTextualMimeType(mimeType string) bool {
	textualTypes := []string{
		"application/json",
		"application/xml",
		"application/yaml",
		"application/x-yaml",
		"application/toml",
		"application/javascript",
		"application/ecmascript",
		"message/",
	}
	return slices.Contains(textualTypes, mimeType)
}
