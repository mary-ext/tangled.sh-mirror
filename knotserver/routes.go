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
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gliderlabs/ssh"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/russross/blackfriday/v2"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/knotserver/git"
)

func (h *Handle) Index(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("This is a knot, part of the wider Tangle network: https://knots.sh"))
}

func (h *Handle) RepoIndex(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(h.c.Repo.ScanPath, didPath(r))

	gr, err := git.Open(path, "")
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			writeMsg(w, "repo empty")
			return
		} else {
			log.Println(err)
			notFound(w)
			return
		}
	}
	commits, err := gr.Commits()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		log.Println(err)
		return
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
		log.Printf("no readme found for %s", path)
	}

	mainBranch, err := gr.FindMainBranch(h.c.Repo.MainBranch)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		log.Println(err)
		return
	}

	if len(commits) >= 3 {
		commits = commits[:3]
	}
	data := make(map[string]any)
	data["ref"] = mainBranch
	data["readme"] = readmeContent
	data["commits"] = commits
	data["desc"] = getDescription(path)

	writeJSON(w, data)
	return
}

func (h *Handle) RepoTree(w http.ResponseWriter, r *http.Request) {
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")

	path := filepath.Join(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	files, err := gr.FileTree(treePath)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		log.Println(err)
		return
	}

	data := make(map[string]any)
	data["ref"] = ref
	data["parent"] = treePath
	data["desc"] = getDescription(path)
	data["dotdot"] = filepath.Dir(treePath)

	h.listFiles(files, data, w)
	return
}

func (h *Handle) FileContent(w http.ResponseWriter, r *http.Request) {
	var raw bool
	if rawParam, err := strconv.ParseBool(r.URL.Query().Get("raw")); err == nil {
		raw = rawParam
	}

	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")

	path := filepath.Join(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	contents, err := gr.FileContent(treePath)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := make(map[string]any)
	data["ref"] = ref
	data["desc"] = getDescription(path)
	data["path"] = treePath

	safe := sanitize([]byte(contents))

	if raw {
		h.showRaw(string(safe), w)
	} else {
		h.showFile(string(safe), data, w)
	}
}

func (h *Handle) Archive(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	file := chi.URLParam(r, "file")

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

	path := filepath.Join(h.c.Repo.ScanPath, didPath(r))
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
		log.Println(err)
		return
	}

	err = gw.Flush()
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with printing the error.
		log.Println(err)
		return
	}
}

func (h *Handle) Log(w http.ResponseWriter, r *http.Request) {
	fmt.Println(r.URL.Path)
	ref := chi.URLParam(r, "ref")

	path := filepath.Join(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	commits, err := gr.Commits()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		log.Println(err)
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

	data := make(map[string]interface{})
	data["commits"] = commits
	data["ref"] = ref
	data["desc"] = getDescription(path)
	data["log"] = true
	data["total"] = total
	data["page"] = page
	data["per_page"] = pageSize

	writeJSON(w, data)
	return
}

func (h *Handle) Diff(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")

	path := filepath.Join(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, ref)
	if err != nil {
		notFound(w)
		return
	}

	diff, err := gr.Diff()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		log.Println(err)
		return
	}

	data := make(map[string]interface{})

	data["commit"] = diff.Commit
	data["stat"] = diff.Stat
	data["diff"] = diff.Diff
	data["ref"] = ref
	data["desc"] = getDescription(path)

	writeJSON(w, data)
	return
}

func (h *Handle) Refs(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(h.c.Repo.ScanPath, didPath(r))
	gr, err := git.Open(path, "")
	if err != nil {
		notFound(w)
		return
	}

	tags, err := gr.Tags()
	if err != nil {
		// Non-fatal, we *should* have at least one branch to show.
		log.Println(err)
	}

	branches, err := gr.Branches()
	if err != nil {
		log.Println(err)
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := make(map[string]interface{})

	data["branches"] = branches
	data["tags"] = tags
	data["desc"] = getDescription(path)

	writeJSON(w, data)
	return
}

func (h *Handle) Keys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		keys, err := h.db.GetAllPublicKeys()
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			log.Println(err)
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
			log.Printf("adding public key: %s", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}
}

func (h *Handle) NewRepo(w http.ResponseWriter, r *http.Request) {
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

	repoPath := filepath.Join(h.c.Repo.ScanPath, did, name)
	err := git.InitBare(repoPath)
	if err != nil {
		log.Println(err)
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TODO: make this set the initial user as the owner
func (h *Handle) Init(w http.ResponseWriter, r *http.Request) {
	if h.knotInitialized {
		writeError(w, "knot already initialized", http.StatusConflict)
		return
	}

	data := struct {
		Did        string   `json:"did"`
		PublicKeys []string `json:"keys"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if data.Did == "" {
		writeError(w, "did is empty", http.StatusBadRequest)
		return
	}

	if err := h.db.AddDid(data.Did); err == nil {
		for _, k := range data.PublicKeys {
			pk := db.PublicKey{
				Did: data.Did,
			}
			pk.Key = k
			err := h.db.AddPublicKey(pk)
			if err != nil {
				writeError(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	} else {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.js.UpdateDids([]string{data.Did})
	// Signal that the knot is ready
	close(h.init)

	mac := hmac.New(sha256.New, []byte(h.c.Server.Secret))
	mac.Write([]byte("ok"))
	w.Header().Add("X-Signature", hex.EncodeToString(mac.Sum(nil)))

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handle) Health(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}
