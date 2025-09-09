package xrpc

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/git"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) RepoBlob(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	ref := r.URL.Query().Get("ref")
	// ref can be empty (git.Open handles this)

	treePath := r.URL.Query().Get("path")
	if treePath == "" {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("missing path parameter"),
		), http.StatusBadRequest)
		return
	}

	raw := r.URL.Query().Get("raw") == "true"

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("RefNotFound"),
			xrpcerr.WithMessage("repository or ref not found"),
		), http.StatusNotFound)
		return
	}

	contents, err := gr.RawContent(treePath)
	if err != nil {
		x.Logger.Error("file content", "error", err.Error())
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("FileNotFound"),
			xrpcerr.WithMessage("file not found at the specified path"),
		), http.StatusNotFound)
		return
	}

	mimeType := http.DetectContentType(contents)

	if filepath.Ext(treePath) == ".svg" {
		mimeType = "image/svg+xml"
	}

	if raw {
		contentHash := sha256.Sum256(contents)
		eTag := fmt.Sprintf("\"%x\"", contentHash)

		switch {
		case strings.HasPrefix(mimeType, "image/"), strings.HasPrefix(mimeType, "video/"):
			if clientETag := r.Header.Get("If-None-Match"); clientETag == eTag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", eTag)
			w.Header().Set("Content-Type", mimeType)

		case strings.HasPrefix(mimeType, "text/"):
			w.Header().Set("Cache-Control", "public, no-cache")
			// serve all text content as text/plain
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		case isTextualMimeType(mimeType):
			// handle textual application types (json, xml, etc.) as text/plain
			w.Header().Set("Cache-Control", "public, no-cache")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		default:
			x.Logger.Error("attempted to serve disallowed file type", "mimetype", mimeType)
			writeError(w, xrpcerr.NewXrpcError(
				xrpcerr.WithTag("InvalidRequest"),
				xrpcerr.WithMessage("only image, video, and text files can be accessed directly"),
			), http.StatusForbidden)
			return
		}
		w.Write(contents)
		return
	}

	isTextual := func(mt string) bool {
		return strings.HasPrefix(mt, "text/") || isTextualMimeType(mt)
	}

	var content string
	var encoding string

	isBinary := !isTextual(mimeType)

	if isBinary {
		content = base64.StdEncoding.EncodeToString(contents)
		encoding = "base64"
	} else {
		content = string(contents)
		encoding = "utf-8"
	}

	response := tangled.RepoBlob_Output{
		Ref:      ref,
		Path:     treePath,
		Content:  content,
		Encoding: &encoding,
		Size:     &[]int64{int64(len(contents))}[0],
		IsBinary: &isBinary,
	}

	if mimeType != "" {
		response.MimeType = &mimeType
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

// isTextualMimeType returns true if the MIME type represents textual content
// that should be served as text/plain for security reasons
func isTextualMimeType(mimeType string) bool {
	textualTypes := []string{
		"application/json",
		"application/xml",
		"application/yaml",
		"application/x-yaml",
		"application/toml",
		"application/javascript",
		"application/ecmascript",
	}

	return slices.Contains(textualTypes, mimeType)
}
