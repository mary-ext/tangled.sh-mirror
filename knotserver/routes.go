package knotserver

import (
	"compress/gzip"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/icyphox/bild/git"
	"github.com/russross/blackfriday/v2"
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
	data["servername"] = h.c.Server.Name
	data["meta"] = h.c.Meta

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

	data := make(map[string]interface{})
	data["commits"] = commits
	data["meta"] = h.c.Meta
	data["ref"] = ref
	data["desc"] = getDescription(path)
	data["log"] = true

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
	data["meta"] = h.c.Meta
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

	data["meta"] = h.c.Meta
	data["branches"] = branches
	data["tags"] = tags
	data["desc"] = getDescription(path)

	writeJSON(w, data)
	return
}

func (h *Handle) ServeStatic(w http.ResponseWriter, r *http.Request) {
	f := chi.URLParam(r, "file")
	f = filepath.Clean(filepath.Join(h.c.Dirs.Static, f))

	http.ServeFile(w, r, f)
}

// func (h *Handle) Keys(w http.ResponseWriter, r *http.Request) {
// 	session, _ := h.s.Get(r, "bild-session")
// 	did := session.Values["did"].(string)

// 	switch r.Method {
// 	case http.MethodGet:
// 		keys, err := h.db.GetPublicKeys(did)
// 		if err != nil {
// 			h.WriteOOBNotice(w, "keys", "Failed to list keys. Try again later.")
// 			log.Println(err)
// 			return
// 		}

// 		data := make(map[string]interface{})
// 		data["keys"] = keys
// 		if err := h.t.ExecuteTemplate(w, "settings/keys", data); err != nil {
// 			log.Println(err)
// 			return
// 		}
// 	case http.MethodPut:
// 		key := r.FormValue("key")
// 		name := r.FormValue("name")
// 		client, _ := h.auth.AuthorizedClient(r)

// 		_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
// 		if err != nil {
// 			h.WriteOOBNotice(w, "keys", "Invalid public key. Check your formatting and try again.")
// 			log.Printf("parsing public key: %s", err)
// 			return
// 		}

// 		if err := h.db.AddPublicKey(did, name, key); err != nil {
// 			h.WriteOOBNotice(w, "keys", "Failed to add key.")
// 			log.Printf("adding public key: %s", err)
// 			return
// 		}

// 		// store in pds too
// 		resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
// 			Collection: "sh.bild.publicKey",
// 			Repo:       did,
// 			Rkey:       uuid.New().String(),
// 			Record: &lexutil.LexiconTypeDecoder{Val: &shbild.PublicKey{
// 				Created: time.Now().String(),
// 				Key:     key,
// 				Name:    name,
// 			}},
// 		})

// 		// invalid record
// 		if err != nil {
// 			h.WriteOOBNotice(w, "keys", "Invalid inputs. Check your formatting and try again.")
// 			log.Printf("failed to create record: %s", err)
// 			return
// 		}

// 		log.Println("created atproto record: ", resp.Uri)

// 		h.WriteOOBNotice(w, "keys", "Key added!")
// 		return
// 	}
// }

// func (h *Handle) NewRepo(w http.ResponseWriter, r *http.Request) {
// 	session, _ := h.s.Get(r, "bild-session")
// 	did := session.Values["did"].(string)
// 	handle := session.Values["handle"].(string)

// 	switch r.Method {
// 	case http.MethodGet:
// 		if err := h.t.ExecuteTemplate(w, "repo/new", nil); err != nil {
// 			log.Println(err)
// 			return
// 		}
// 	case http.MethodPut:
// 		name := r.FormValue("name")
// 		description := r.FormValue("description")

// 		repoPath := filepath.Join(h.c.Repo.ScanPath, did, name)
// 		err := git.InitBare(repoPath)
// 		if err != nil {
// 			h.WriteOOBNotice(w, "repo", "Error creating repo. Try again later.")
// 			return
// 		}

// 		err = h.db.AddRepo(did, name, description)
// 		if err != nil {
// 			h.WriteOOBNotice(w, "repo", "Error creating repo. Try again later.")
// 			return
// 		}

// 		w.Header().Set("HX-Redirect", fmt.Sprintf("/@%s/%s", handle, name))
// 		w.WriteHeader(http.StatusOK)
// 	}
// }

// func (h *Handle) Timeline(w http.ResponseWriter, r *http.Request) {
// 	session, err := h.s.Get(r, "bild-session")
// 	user := make(map[string]string)
// 	if err != nil || session.IsNew {
// 		// user is not logged in
// 	} else {
// 		user["handle"] = session.Values["handle"].(string)
// 		user["did"] = session.Values["did"].(string)
// 	}

// 	if err := h.t.ExecuteTemplate(w, "timeline", user); err != nil {
// 		log.Println(err)
// 		return
// 	}
// }
