package knotserver

import (
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/gliderlabs/ssh"
	"github.com/go-chi/chi/v5"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/russross/blackfriday/v2"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/knotserver/git"
	"github.com/sotangled/tangled/types"
)

func (h *Handle) Index(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("This is a knot server. More info at https://tangled.sh"))
}

func (h *Handle) RepoIndex(w http.ResponseWriter, r *http.Request) {
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	l := h.l.With("path", path, "handler", "RepoIndex")
	ref := chi.URLParam(r, "ref")

	gr, err := git.Open(path, ref)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			resp := types.RepoIndexResponse{
				IsEmpty: true,
			}
			writeJSON(w, resp)
			return
		} else {
			l.Error("opening repo", "error", err.Error())
			notFound(w)
			return
		}
	}

	commits, err := gr.Commits()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("fetching commits", "error", err.Error())
		return
	}
	if len(commits) > 10 {
		commits = commits[:10]
	}

	branches, err := gr.Branches()
	if err != nil {
		l.Error("getting branches", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bs := []types.Branch{}
	for _, branch := range branches {
		b := types.Branch{}
		b.Hash = branch.Hash().String()
		b.Name = branch.Name().Short()
		bs = append(bs, b)
	}

	tags, err := gr.Tags()
	if err != nil {
		// Non-fatal, we *should* have at least one branch to show.
		l.Warn("getting tags", "error", err.Error())
	}

	rtags := []*types.TagReference{}
	for _, tag := range tags {
		tr := types.TagReference{
			Tag: tag.TagObject(),
		}

		tr.Reference = types.Reference{
			Name: tag.Name(),
			Hash: tag.Hash().String(),
		}

		if tag.Message() != "" {
			tr.Message = tag.Message()
		}

		rtags = append(rtags, &tr)
	}

	var readmeContent template.HTML
	for _, readme := range h.c.Repo.Readme {
		ext := filepath.Ext(readme)
		content, _ := gr.FileContent(readme)
		if len(content) > 0 {
			switch ext {
			case ".md", ".mkd", ".markdown":
				unsafe := blackfriday.Run(
					[]byte(content),
					blackfriday.WithExtensions(blackfriday.CommonExtensions),
				)
				html := sanitize(unsafe)
				readmeContent = template.HTML(html)
			default:
				safe := sanitize([]byte(content))
				readmeContent = template.HTML(
					fmt.Sprintf(`<pre>%s</pre>`, safe),
				)
			}
			break
		}
	}

	if readmeContent == "" {
		l.Warn("no readme found")
	}

	files, err := gr.FileTree("")
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("file tree", "error", err.Error())
		return
	}

	if ref == "" {
		mainBranch, err := gr.FindMainBranch(h.c.Repo.MainBranch)
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			l.Error("finding main branch", "error", err.Error())
			return
		}
		ref = mainBranch
	}

	resp := types.RepoIndexResponse{
		IsEmpty:     false,
		Ref:         ref,
		Commits:     commits,
		Description: getDescription(path),
		Readme:      readmeContent,
		Files:       files,
		Branches:    bs,
		Tags:        rtags,
	}

	writeJSON(w, resp)
	return
}

func (h *Handle) RepoTree(w http.ResponseWriter, r *http.Request) {
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")

	l := h.l.With("handler", "RepoTree", "ref", ref, "treePath", treePath)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	files, err := gr.FileTree(treePath)
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

func (h *Handle) Blob(w http.ResponseWriter, r *http.Request) {
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")

	l := h.l.With("handler", "FileContent", "ref", ref, "treePath", treePath)

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

	// This allows the browser to use a proper name for the file when
	// downloading
	filename := fmt.Sprintf("%s-%s.tar.gz", name, ref)
	setContentDisposition(w, filename)
	setGZipMIME(w)

	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	gw := gzip.NewWriter(w)
	defer gw.Close()

	prefix := fmt.Sprintf("%s-%s", name, ref)
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
	path, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, didPath(r))

	l := h.l.With("handler", "Log", "ref", ref, "path", path)

	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	commits, err := gr.Commits()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		l.Error("fetching commits", "error", err.Error())
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

	// Calculate pagination
	start := (page - 1) * pageSize
	end := start + pageSize
	total := len(commits)

	if start >= total {
		commits = []*object.Commit{}
	} else {
		if end > total {
			end = total
		}
		commits = commits[start:end]
	}

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
		tr := types.TagReference{
			Tag: tag.TagObject(),
		}

		tr.Reference = types.Reference{
			Name: tag.Name(),
			Hash: tag.Hash().String(),
		}

		if tag.Message() != "" {
			tr.Message = tag.Message()
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
	l := h.l.With("handler", "Branches")

	gr, err := git.Open(path, "")
	if err != nil {
		notFound(w)
		return
	}

	branches, err := gr.Branches()
	if err != nil {
		l.Error("getting branches", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bs := []types.Branch{}
	for _, branch := range branches {
		b := types.Branch{}
		b.Hash = branch.Hash().String()
		b.Name = branch.Name().Short()
		bs = append(bs, b)
	}

	resp := types.RepoBranchesResponse{
		Branches: bs,
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

		data := make([]map[string]interface{}, 0)
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

func (h *Handle) NewRepo(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "NewRepo")

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

	relativeRepoPath := filepath.Join(did, name)
	repoPath, _ := securejoin.SecureJoin(h.c.Repo.ScanPath, relativeRepoPath)
	err := git.InitBare(repoPath)
	if err != nil {
		l.Error("initializing bare repo", "error", err.Error())
		if errors.Is(err, gogit.ErrRepositoryAlreadyExists) {
			writeError(w, "That repo already exists!", http.StatusConflict)
			return
		} else {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// add perms for this user to access the repo
	err = h.e.AddRepo(did, ThisServer, relativeRepoPath)
	if err != nil {
		l.Error("adding repo permissions", "error", err.Error())
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
	if err := h.e.AddMember(ThisServer, did); err != nil {
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
	if err := h.e.AddCollaborator(data.Did, ThisServer, repoName); err != nil {
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

	h.jc.UpdateDids([]string{data.Did})
	if err := h.e.AddOwner(ThisServer, data.Did); err != nil {
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
