package knotserver

import (
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/gliderlabs/ssh"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.sh/tangled.sh/core/hook"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/types"
)

func (h *Handle) Index(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("This is a knot server. More info at https://tangled.sh"))
}

func (h *Handle) Capabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	capabilities := map[string]any{
		"pull_requests": map[string]any{
			"format_patch":       true,
			"patch_submissions":  true,
			"branch_submissions": true,
			"fork_submissions":   true,
		},
	}

	jsonData, err := json.Marshal(capabilities)
	if err != nil {
		http.Error(w, "Failed to serialize JSON", http.StatusInternalServerError)
		return
	}

	w.Write(jsonData)
}

func (h *Handle) RepoIndex(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	l := h.l.With("path", path, "handler", "RepoIndex")
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	gr, err := git.Open(path, ref)
	if err != nil {
		plain, err2 := git.PlainOpen(path)
		if err2 != nil {
			l.Error("opening repo", "error", err2.Error())
			notFound(w)
			return
		}
		branches, _ := plain.Branches()

		log.Println(err)

		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			resp := types.RepoIndexResponse{
				IsEmpty:  true,
				Branches: branches,
			}
			writeJSON(w, resp)
			return
		} else {
			l.Error("opening repo", "error", err.Error())
			notFound(w)
			return
		}
	}

	var (
		commits  []*object.Commit
		total    int
		branches []types.Branch
		files    []types.NiceTree
		tags     []object.Tag
	)

	var wg sync.WaitGroup
	errorsCh := make(chan error, 5)

	wg.Add(1)
	go func() {
		defer wg.Done()
		cs, err := gr.Commits(0, 60)
		if err != nil {
			errorsCh <- fmt.Errorf("commits: %w", err)
			return
		}
		commits = cs
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		t, err := gr.TotalCommits()
		if err != nil {
			errorsCh <- fmt.Errorf("calculating total: %w", err)
			return
		}
		total = t
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		bs, err := gr.Branches()
		if err != nil {
			errorsCh <- fmt.Errorf("fetching branches: %w", err)
			return
		}
		branches = bs
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ts, err := gr.Tags()
		if err != nil {
			errorsCh <- fmt.Errorf("fetching tags: %w", err)
			return
		}
		tags = ts
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fs, err := gr.FileTree(r.Context(), "")
		if err != nil {
			errorsCh <- fmt.Errorf("fetching filetree: %w", err)
			return
		}
		files = fs
	}()

	wg.Wait()
	close(errorsCh)

	// show any errors
	for err := range errorsCh {
		l.Error("loading repo", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rtags := []*types.TagReference{}
	for _, tag := range tags {
		var target *object.Tag
		if tag.Target != plumbing.ZeroHash {
			target = &tag
		}
		tr := types.TagReference{
			Tag: target,
		}

		tr.Reference = types.Reference{
			Name: tag.Name,
			Hash: tag.Hash.String(),
		}

		if tag.Message != "" {
			tr.Message = tag.Message
		}

		rtags = append(rtags, &tr)
	}

	var readmeContent string
	var readmeFile string
	for _, readme := range h.c.Repo.Readme {
		content, _ := gr.FileContent(readme)
		if len(content) > 0 {
			readmeContent = string(content)
			readmeFile = readme
		}
	}

	if ref == "" {
		mainBranch, err := gr.FindMainBranch()
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			l.Error("finding main branch", "error", err.Error())
			return
		}
		ref = mainBranch
	}

	resp := types.RepoIndexResponse{
		IsEmpty:        false,
		Ref:            ref,
		Commits:        commits,
		Description:    getDescription(path),
		Readme:         readmeContent,
		ReadmeFileName: readmeFile,
		Files:          files,
		Branches:       branches,
		Tags:           rtags,
		TotalCommits:   total,
	}

	writeJSON(w, resp)
	return
}

func (h *Handle) RepoTree(w http.ResponseWriter, r *http.Request) {
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	l := h.l.With("handler", "RepoTree", "ref", ref, "treePath", treePath)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	files, err := gr.FileTree(r.Context(), treePath)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("file tree", "error", err.Error())
		return
	}

	resp := types.RepoTreeResponse{
		Ref:         ref,
		Parent:      treePath,
		Description: getDescription(path),
		DotDot:      filepath.Dir(treePath),
		Files:       files,
	}

	writeJSON(w, resp)
	return
}

func (h *Handle) BlobRaw(w http.ResponseWriter, r *http.Request) {
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	l := h.l.With("handler", "BlobRaw", "ref", ref, "treePath", treePath)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	contents, err := gr.RawContent(treePath)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		l.Error("file content", "error", err.Error())
		return
	}

	mimeType := http.DetectContentType(contents)

	// exception for svg
	if filepath.Ext(treePath) == ".svg" {
		mimeType = "image/svg+xml"
	}

	contentHash := sha256.Sum256(contents)
	eTag := fmt.Sprintf("\"%x\"", contentHash)

	// allow image, video, and text/plain files to be served directly
	switch {
	case strings.HasPrefix(mimeType, "image/"), strings.HasPrefix(mimeType, "video/"):
		if clientETag := r.Header.Get("If-None-Match"); clientETag == eTag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", eTag)

	case strings.HasPrefix(mimeType, "text/plain"):
		w.Header().Set("Cache-Control", "public, no-cache")

	default:
		l.Error("attempted to serve disallowed file type", "mimetype", mimeType)
		writeError(w, "only image, video, and text files can be accessed directly", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.Write(contents)
}

func (h *Handle) Blob(w http.ResponseWriter, r *http.Request) {
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	l := h.l.With("handler", "Blob", "ref", ref, "treePath", treePath)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	var isBinaryFile bool = false
	contents, err := gr.FileContent(treePath)
	if errors.Is(err, git.ErrBinaryFile) {
		isBinaryFile = true
	} else if errors.Is(err, object.ErrFileNotFound) {
		notFound(w)
		return
	} else if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bytes := []byte(contents)
	// safe := string(sanitize(bytes))
	sizeHint := len(bytes)

	resp := types.RepoBlobResponse{
		Ref:      ref,
		Contents: string(bytes),
		Path:     treePath,
		IsBinary: isBinaryFile,
		SizeHint: uint64(sizeHint),
	}

	h.showFile(resp, w, l)
}

func (h *Handle) Archive(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	file := chi.URLParam(r, "file")

	l := h.l.With("handler", "Archive", "name", name, "file", file)

	// TODO: extend this to add more files compression (e.g.: xz)
	if !strings.HasSuffix(file, ".tar.gz") {
		notFound(w)
		return
	}

	ref := strings.TrimSuffix(file, ".tar.gz")

	unescapedRef, err := url.PathUnescape(ref)
	if err != nil {
		notFound(w)
		return
	}

	safeRefFilename := strings.ReplaceAll(plumbing.ReferenceName(unescapedRef).Short(), "/", "-")

	// This allows the browser to use a proper name for the file when
	// downloading
	filename := fmt.Sprintf("%s-%s.tar.gz", name, safeRefFilename)
	setContentDisposition(w, filename)
	setGZipMIME(w)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, unescapedRef)
	if err != nil {
		notFound(w)
		return
	}

	gw := gzip.NewWriter(w)
	defer gw.Close()

	prefix := fmt.Sprintf("%s-%s", name, safeRefFilename)
	err = gr.WriteTar(gw, prefix)
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with printing the error.
		l.Error("writing tar file", "error", err.Error())
		return
	}

	err = gw.Flush()
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with printing the error.
		l.Error("flushing?", "error", err.Error())
		return
	}
}

func (h *Handle) Log(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	l := h.l.With("handler", "Log", "ref", ref, "path", path)

	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	// Get page parameters
	page := 1
	pageSize := 30

	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if p, err := strconv.Atoi(pageParam); err == nil && p > 0 {
			page = p
		}
	}

	if pageSizeParam := r.URL.Query().Get("per_page"); pageSizeParam != "" {
		if ps, err := strconv.Atoi(pageSizeParam); err == nil && ps > 0 {
			pageSize = ps
		}
	}

	// convert to offset/limit
	offset := (page - 1) * pageSize
	limit := pageSize

	commits, err := gr.Commits(offset, limit)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("fetching commits", "error", err.Error())
		return
	}

	total := len(commits)

	resp := types.RepoLogResponse{
		Commits:     commits,
		Ref:         ref,
		Description: getDescription(path),
		Log:         true,
		Total:       total,
		Page:        page,
		PerPage:     pageSize,
	}

	writeJSON(w, resp)
	return
}

func (h *Handle) Diff(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	l := h.l.With("handler", "Diff", "ref", ref)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	diff, err := gr.Diff()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("getting diff", "error", err.Error())
		return
	}

	resp := types.RepoCommitResponse{
		Ref:  ref,
		Diff: diff,
	}

	writeJSON(w, resp)
	return
}

func (h *Handle) Tags(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	l := h.l.With("handler", "Refs")

	gr, err := git.Open(path, "")
	if err != nil {
		notFound(w)
		return
	}

	tags, err := gr.Tags()
	if err != nil {
		// Non-fatal, we *should* have at least one branch to show.
		l.Warn("getting tags", "error", err.Error())
	}

	rtags := []*types.TagReference{}
	for _, tag := range tags {
		var target *object.Tag
		if tag.Target != plumbing.ZeroHash {
			target = &tag
		}
		tr := types.TagReference{
			Tag: target,
		}

		tr.Reference = types.Reference{
			Name: tag.Name,
			Hash: tag.Hash.String(),
		}

		if tag.Message != "" {
			tr.Message = tag.Message
		}

		rtags = append(rtags, &tr)
	}

	resp := types.RepoTagsResponse{
		Tags: rtags,
	}

	writeJSON(w, resp)
	return
}

func (h *Handle) Branches(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	gr, err := git.PlainOpen(path)
	if err != nil {
		notFound(w)
		return
	}

	branches, _ := gr.Branches()

	resp := types.RepoBranchesResponse{
		Branches: branches,
	}

	writeJSON(w, resp)
	return
}

func (h *Handle) Branch(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	branchName := chi.URLParam(r, "branch")
	branchName, _ = url.PathUnescape(branchName)

	l := h.l.With("handler", "Branch")

	gr, err := git.PlainOpen(path)
	if err != nil {
		notFound(w)
		return
	}

	ref, err := gr.Branch(branchName)
	if err != nil {
		l.Error("getting branch", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	commit, err := gr.Commit(ref.Hash())
	if err != nil {
		l.Error("getting commit object", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defaultBranch, err := gr.FindMainBranch()
	isDefault := false
	if err != nil {
		l.Error("getting default branch", "error", err.Error())
		// do not quit though
	} else if defaultBranch == branchName {
		isDefault = true
	}

	resp := types.RepoBranchResponse{
		Branch: types.Branch{
			Reference: types.Reference{
				Name: ref.Name().Short(),
				Hash: ref.Hash().String(),
			},
			Commit:    commit,
			IsDefault: isDefault,
		},
	}

	writeJSON(w, resp)
	return
}

func (h *Handle) Keys(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "Keys")

	switch r.Method {
	case http.MethodGet:
		keys, err := h.db.GetAllPublicKeys()
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			l.Error("getting public keys", "error", err.Error())
			return
		}

		data := make([]map[string]any, 0)
		for _, key := range keys {
			j := key.JSON()
			data = append(data, j)
		}
		writeJSON(w, data)
		return

	case http.MethodPut:
		pk := db.PublicKey{}
		if err := json.NewDecoder(r.Body).Decode(&pk); err != nil {
			writeError(w, "invalid request body", http.StatusBadRequest)
			return
		}

		_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pk.Key))
		if err != nil {
			writeError(w, "invalid pubkey", http.StatusBadRequest)
		}

		if err := h.db.AddPublicKey(pk); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			l.Error("adding public key", "error", err.Error())
			return
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}
}

func (h *Handle) RepoForkAheadBehind(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "RepoForkAheadBehind")

	data := struct {
		Did       string `json:"did"`
		Source    string `json:"source"`
		Name      string `json:"name,omitempty"`
		HiddenRef string `json:"hiddenref"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	did := data.Did
	source := data.Source

	if did == "" || source == "" {
		l.Error("invalid request body, empty did or name")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var name string
	if data.Name != "" {
		name = data.Name
	} else {
		name = filepath.Base(source)
	}

	branch := chi.URLParam(r, "branch")
	branch, _ = url.PathUnescape(branch)

	relativeRepoPath := filepath.Join(did, name)
	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)

	gr, err := git.PlainOpen(repoPath)
	if err != nil {
		log.Println(err)
		notFound(w)
		return
	}

	forkCommit, err := gr.ResolveRevision(branch)
	if err != nil {
		l.Error("error resolving ref revision", "msg", err.Error())
		writeError(w, fmt.Sprintf("error resolving revision %s", branch), http.StatusBadRequest)
		return
	}

	sourceCommit, err := gr.ResolveRevision(data.HiddenRef)
	if err != nil {
		l.Error("error resolving hidden ref revision", "msg", err.Error())
		writeError(w, fmt.Sprintf("error resolving revision %s", data.HiddenRef), http.StatusBadRequest)
		return
	}

	status := types.UpToDate
	if forkCommit.Hash.String() != sourceCommit.Hash.String() {
		isAncestor, err := forkCommit.IsAncestor(sourceCommit)
		if err != nil {
			log.Printf("error resolving whether %s is ancestor of %s: %s", branch, data.HiddenRef, err)
			return
		}

		if isAncestor {
			status = types.FastForwardable
		} else {
			status = types.Conflict
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.AncestorCheckResponse{Status: status})
}

func (h *Handle) RepoLanguages(w http.ResponseWriter, r *http.Request) {
	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	l := h.l.With("handler", "RepoLanguages")

	gr, err := git.Open(repoPath, ref)
	if err != nil {
		l.Error("opening repo", "error", err.Error())
		notFound(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	sizes, err := gr.AnalyzeLanguages(ctx)
	if err != nil {
		l.Error("failed to analyze languages", "error", err.Error())
		writeError(w, err.Error(), http.StatusNoContent)
		return
	}

	resp := types.RepoLanguageResponse{Languages: sizes}

	writeJSON(w, resp)
}

func (h *Handle) RepoForkSync(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "RepoForkSync")

	data := struct {
		Did    string `json:"did"`
		Source string `json:"source"`
		Name   string `json:"name,omitempty"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	did := data.Did
	source := data.Source

	if did == "" || source == "" {
		l.Error("invalid request body, empty did or name")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var name string
	if data.Name != "" {
		name = data.Name
	} else {
		name = filepath.Base(source)
	}

	branch := chi.URLParam(r, "*")
	branch, _ = url.PathUnescape(branch)

	relativeRepoPath := filepath.Join(did, name)
	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)

	gr, err := git.Open(repoPath, branch)
	if err != nil {
		log.Println(err)
		notFound(w)
		return
	}

	err = gr.Sync()
	if err != nil {
		l.Error("error syncing repo fork", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handle) RepoFork(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "RepoFork")

	data := struct {
		Did    string `json:"did"`
		Source string `json:"source"`
		Name   string `json:"name,omitempty"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	did := data.Did
	source := data.Source

	if did == "" || source == "" {
		l.Error("invalid request body, empty did or name")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var name string
	if data.Name != "" {
		name = data.Name
	} else {
		name = filepath.Base(source)
	}

	relativeRepoPath := filepath.Join(did, name)
	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)

	err := git.Fork(repoPath, source)
	if err != nil {
		l.Error("forking repo", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// add perms for this user to access the repo
	err = h.e.AddRepo(did, rbac.ThisServer, relativeRepoPath)
	if err != nil {
		l.Error("adding repo permissions", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hook.SetupRepo(
		hook.Config(
			hook.WithScanPath(h.c.Repo.ScanPath),
			hook.WithInternalApi(h.c.Server.InternalListenAddr),
		),
		repoPath,
	)

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handle) RemoveRepo(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "RemoveRepo")

	data := struct {
		Did  string `json:"did"`
		Name string `json:"name"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	did := data.Did
	name := data.Name

	if did == "" || name == "" {
		l.Error("invalid request body, empty did or name")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	relativeRepoPath := filepath.Join(did, name)
	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)
	err := os.RemoveAll(repoPath)
	if err != nil {
		l.Error("removing repo", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)

}
func (h *Handle) Merge(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	data := types.MergeRequest{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		h.l.Error("git: failed to unmarshal json patch", "handler", "Merge", "error", err)
		return
	}

	mo := &git.MergeOptions{
		AuthorName:    data.AuthorName,
		AuthorEmail:   data.AuthorEmail,
		CommitBody:    data.CommitBody,
		CommitMessage: data.CommitMessage,
	}

	patch := data.Patch
	branch := data.Branch
	gr, err := git.Open(path, branch)
	if err != nil {
		notFound(w)
		return
	}

	mo.FormatPatch = patchutil.IsFormatPatch(patch)

	if err := gr.MergeWithOptions([]byte(patch), branch, mo); err != nil {
		var mergeErr *git.ErrMerge
		if errors.As(err, &mergeErr) {
			conflicts := make([]types.ConflictInfo, len(mergeErr.Conflicts))
			for i, conflict := range mergeErr.Conflicts {
				conflicts[i] = types.ConflictInfo{
					Filename: conflict.Filename,
					Reason:   conflict.Reason,
				}
			}
			response := types.MergeCheckResponse{
				IsConflicted: true,
				Conflicts:    conflicts,
				Message:      mergeErr.Message,
			}
			writeConflict(w, response)
			h.l.Error("git: merge conflict", "handler", "Merge", "error", mergeErr)
		} else {
			writeError(w, err.Error(), http.StatusBadRequest)
			h.l.Error("git: failed to merge", "handler", "Merge", "error", err.Error())
		}
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handle) MergeCheck(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	var data struct {
		Patch  string `json:"patch"`
		Branch string `json:"branch"`
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		h.l.Error("git: failed to unmarshal json patch", "handler", "MergeCheck", "error", err)
		return
	}

	patch := data.Patch
	branch := data.Branch
	gr, err := git.Open(path, branch)
	if err != nil {
		notFound(w)
		return
	}

	err = gr.MergeCheck([]byte(patch), branch)
	if err == nil {
		response := types.MergeCheckResponse{
			IsConflicted: false,
		}
		writeJSON(w, response)
		return
	}

	var mergeErr *git.ErrMerge
	if errors.As(err, &mergeErr) {
		conflicts := make([]types.ConflictInfo, len(mergeErr.Conflicts))
		for i, conflict := range mergeErr.Conflicts {
			conflicts[i] = types.ConflictInfo{
				Filename: conflict.Filename,
				Reason:   conflict.Reason,
			}
		}
		response := types.MergeCheckResponse{
			IsConflicted: true,
			Conflicts:    conflicts,
			Message:      mergeErr.Message,
		}
		writeConflict(w, response)
		h.l.Error("git: merge conflict", "handler", "MergeCheck", "error", mergeErr.Error())
		return
	}
	writeError(w, err.Error(), http.StatusInternalServerError)
	h.l.Error("git: failed to check merge", "handler", "MergeCheck", "error", err.Error())
}

func (h *Handle) Compare(w http.ResponseWriter, r *http.Request) {
	rev1 := chi.URLParam(r, "rev1")
	rev1, _ = url.PathUnescape(rev1)

	rev2 := chi.URLParam(r, "rev2")
	rev2, _ = url.PathUnescape(rev2)

	l := h.l.With("handler", "Compare", "r1", rev1, "r2", rev2)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.PlainOpen(path)
	if err != nil {
		notFound(w)
		return
	}

	commit1, err := gr.ResolveRevision(rev1)
	if err != nil {
		l.Error("error resolving revision 1", "msg", err.Error())
		writeError(w, fmt.Sprintf("error resolving revision %s", rev1), http.StatusBadRequest)
		return
	}

	commit2, err := gr.ResolveRevision(rev2)
	if err != nil {
		l.Error("error resolving revision 2", "msg", err.Error())
		writeError(w, fmt.Sprintf("error resolving revision %s", rev2), http.StatusBadRequest)
		return
	}

	rawPatch, formatPatch, err := gr.FormatPatch(commit1, commit2)
	if err != nil {
		l.Error("error comparing revisions", "msg", err.Error())
		writeError(w, "error comparing revisions", http.StatusBadRequest)
		return
	}

	writeJSON(w, types.RepoFormatPatchResponse{
		Rev1:        commit1.Hash.String(),
		Rev2:        commit2.Hash.String(),
		FormatPatch: formatPatch,
		Patch:       rawPatch,
	})
	return
}

func (h *Handle) NewHiddenRef(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "NewHiddenRef")

	forkRef := chi.URLParam(r, "forkRef")
	forkRef, _ = url.PathUnescape(forkRef)

	remoteRef := chi.URLParam(r, "remoteRef")
	remoteRef, _ = url.PathUnescape(remoteRef)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.PlainOpen(path)
	if err != nil {
		notFound(w)
		return
	}

	err = gr.TrackHiddenRemoteRef(forkRef, remoteRef)
	if err != nil {
		l.Error("error tracking hidden remote ref", "msg", err.Error())
		writeError(w, "error tracking hidden remote ref", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	return
}

func (h *Handle) AddMember(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "AddMember")

	data := struct {
		Did string `json:"did"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	did := data.Did

	if err := h.db.AddDid(did); err != nil {
		l.Error("adding did", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jc.AddDid(did)

	if err := h.e.AddKnotMember(rbac.ThisServer, did); err != nil {
		l.Error("adding member", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.fetchAndAddKeys(r.Context(), did); err != nil {
		l.Error("fetching and adding keys", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handle) AddRepoCollaborator(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "AddRepoCollaborator")

	data := struct {
		Did string `json:"did"`
	}{}

	ownerDid := chi.URLParam(r, "did")
	repo := chi.URLParam(r, "name")

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.db.AddDid(data.Did); err != nil {
		l.Error("adding did", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jc.AddDid(data.Did)

	repoName, _ := securejoin.SecureJoin(ownerDid, repo)
	if err := h.e.AddCollaborator(data.Did, rbac.ThisServer, repoName); err != nil {
		l.Error("adding repo collaborator", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.fetchAndAddKeys(r.Context(), data.Did); err != nil {
		l.Error("fetching and adding keys", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handle) DefaultBranch(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "DefaultBranch")
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	gr, err := git.Open(path, "")
	if err != nil {
		notFound(w)
		return
	}

	branch, err := gr.FindMainBranch()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("getting default branch", "error", err.Error())
		return
	}

	writeJSON(w, types.RepoDefaultBranchResponse{
		Branch: branch,
	})
}

func (h *Handle) SetDefaultBranch(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "SetDefaultBranch")
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	data := struct {
		Branch string `json:"branch"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	gr, err := git.PlainOpen(path)
	if err != nil {
		notFound(w)
		return
	}

	err = gr.SetDefaultBranch(data.Branch)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("setting default branch", "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handle) Init(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "Init")

	if h.knotInitialized {
		writeError(w, "knot already initialized", http.StatusConflict)
		return
	}

	data := struct {
		Did string `json:"did"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		l.Error("failed to decode request body", "error", err.Error())
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if data.Did == "" {
		l.Error("empty DID in request", "did", data.Did)
		writeError(w, "did is empty", http.StatusBadRequest)
		return
	}

	if err := h.db.AddDid(data.Did); err != nil {
		l.Error("failed to add DID", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jc.AddDid(data.Did)

	if err := h.e.AddKnotOwner(rbac.ThisServer, data.Did); err != nil {
		l.Error("adding owner", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.fetchAndAddKeys(r.Context(), data.Did); err != nil {
		l.Error("fetching and adding keys", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	close(h.init)

	mac := hmac.New(sha256.New, []byte(h.c.Server.Secret))
	mac.Write([]byte("ok"))
	w.Header().Add("X-Signature", hex.EncodeToString(mac.Sum(nil)))

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handle) Health(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func validateRepoName(name string) error {
	// check for path traversal attempts
	if name == "." || name == ".." ||
		strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("Repository name contains invalid path characters")
	}

	// check for sequences that could be used for traversal when normalized
	if strings.Contains(name, "./") || strings.Contains(name, "../") ||
		strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("Repository name contains invalid path sequence")
	}

	// then continue with character validation
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == '.') {
			return fmt.Errorf("Repository name can only contain alphanumeric characters, periods, hyphens, and underscores")
		}
	}

	// additional check to prevent multiple sequential dots
	if strings.Contains(name, "..") {
		return fmt.Errorf("Repository name cannot contain sequential dots")
	}

	// if all checks pass
	return nil
}
