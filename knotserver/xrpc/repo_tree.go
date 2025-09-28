package xrpc

import (
	"net/http"
	"path/filepath"
	"time"
	"unicode/utf8"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/knotserver/git"
	xrpcerr "tangled.org/core/xrpc/errors"
)

func (x *Xrpc) RepoTree(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	repo := r.URL.Query().Get("repo")
	repoPath, err := x.parseRepoParam(repo)
	if err != nil {
		writeError(w, err.(xrpcerr.XrpcError), http.StatusBadRequest)
		return
	}

	ref := r.URL.Query().Get("ref")
	// ref can be empty (git.Open handles this)

	path := r.URL.Query().Get("path")
	// path can be empty (defaults to root)

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		x.Logger.Error("failed to open git repository", "error", err, "path", repoPath, "ref", ref)
		writeError(w, xrpcerr.RefNotFoundError, http.StatusNotFound)
		return
	}

	files, err := gr.FileTree(ctx, path)
	if err != nil {
		x.Logger.Error("failed to get file tree", "error", err, "path", path)
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("PathNotFound"),
			xrpcerr.WithMessage("failed to read repository tree"),
		), http.StatusNotFound)
		return
	}

	// if any of these files are a readme candidate, pass along its blob contents too
	var readmeFileName string
	var readmeContents string
	for _, file := range files {
		if markup.IsReadmeFile(file.Name) {
			contents, err := gr.RawContent(filepath.Join(path, file.Name))
			if err != nil {
				x.Logger.Error("failed to read contents of file", "path", path, "file", file.Name)
			}

			if utf8.Valid(contents) {
				readmeFileName = file.Name
				readmeContents = string(contents)
				break
			}
		}
	}

	// convert NiceTree -> tangled.RepoTree_TreeEntry
	treeEntries := make([]*tangled.RepoTree_TreeEntry, len(files))
	for i, file := range files {
		entry := &tangled.RepoTree_TreeEntry{
			Name:       file.Name,
			Mode:       file.Mode,
			Size:       file.Size,
			Is_file:    file.IsFile,
			Is_subtree: file.IsSubtree,
		}

		if file.LastCommit != nil {
			entry.Last_commit = &tangled.RepoTree_LastCommit{
				Hash:    file.LastCommit.Hash.String(),
				Message: file.LastCommit.Message,
				When:    file.LastCommit.When.Format(time.RFC3339),
			}
		}

		treeEntries[i] = entry
	}

	var parentPtr *string
	if path != "" {
		parentPtr = &path
	}

	var dotdotPtr *string
	if path != "" {
		dotdot := filepath.Dir(path)
		if dotdot != "." {
			dotdotPtr = &dotdot
		}
	}

	response := tangled.RepoTree_Output{
		Ref:    ref,
		Parent: parentPtr,
		Dotdot: dotdotPtr,
		Files:  treeEntries,
		Readme: &tangled.RepoTree_Readme{
			Filename: readmeFileName,
			Contents: readmeContents,
		},
	}

	writeJson(w, response)
}
