package routes

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/dustin/go-humanize"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/gorilla/sessions"
	"github.com/icyphox/bild/legit/config"
	"github.com/icyphox/bild/legit/db"
	"github.com/icyphox/bild/legit/git"
	"github.com/russross/blackfriday/v2"
	"golang.org/x/crypto/ssh"
)

type Handle struct {
	c  *config.Config
	t  *template.Template
	s  *sessions.CookieStore
	db *db.DB
}

func (h *Handle) Index(w http.ResponseWriter, r *http.Request) {
	user := chi.URLParam(r, "user")
	path := filepath.Join(h.c.Repo.ScanPath, user)
	dirs, err := os.ReadDir(path)
	if err != nil {
		h.Write500(w)
		log.Printf("reading scan path: %s", err)
		return
	}

	type info struct {
		DisplayName, Name, Desc, Idle string
		d                             time.Time
	}

	infos := []info{}

	for _, dir := range dirs {
		name := dir.Name()
		if !dir.IsDir() || h.isIgnored(name) || h.isUnlisted(name) {
			continue
		}

		gr, err := git.Open(path, "")
		if err != nil {
			log.Println(err)
			continue
		}

		c, err := gr.LastCommit()
		if err != nil {
			h.Write500(w)
			log.Println(err)
			return
		}

		infos = append(infos, info{
			DisplayName: getDisplayName(name),
			Name:        name,
			Desc:        getDescription(path),
			Idle:        humanize.Time(c.Author.When),
			d:           c.Author.When,
		})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[j].d.Before(infos[i].d)
	})

	data := make(map[string]interface{})
	data["meta"] = h.c.Meta
	data["info"] = infos

	if err := h.t.ExecuteTemplate(w, "index", data); err != nil {
		log.Println(err)
		return
	}
}

func (h *Handle) RepoIndex(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if h.isIgnored(name) {
		h.Write404(w)
		return
	}

	name = filepath.Clean(name)
	path := filepath.Join(h.c.Repo.ScanPath, name)

	gr, err := git.Open(path, "")
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			h.t.ExecuteTemplate(w, "repo/empty", nil)
			return
		} else {
			h.Write404(w)
			return
		}
	}
	commits, err := gr.Commits()
	if err != nil {
		h.Write500(w)
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
		log.Printf("no readme found for %s", name)
	}

	mainBranch, err := gr.FindMainBranch(h.c.Repo.MainBranch)
	if err != nil {
		h.Write500(w)
		log.Println(err)
		return
	}

	if len(commits) >= 3 {
		commits = commits[:3]
	}

	data := make(map[string]any)
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = mainBranch
	data["readme"] = readmeContent
	data["commits"] = commits
	data["desc"] = getDescription(path)
	data["servername"] = h.c.Server.Name
	data["meta"] = h.c.Meta
	data["gomod"] = isGoModule(gr)

	if err := h.t.ExecuteTemplate(w, "repo/repo", data); err != nil {
		log.Println(err)
		return
	}

	return
}

func (h *Handle) RepoTree(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if h.isIgnored(name) {
		h.Write404(w)
		return
	}
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")

	name = filepath.Clean(name)
	path := filepath.Join(h.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		h.Write404(w)
		return
	}

	files, err := gr.FileTree(treePath)
	if err != nil {
		h.Write500(w)
		log.Println(err)
		return
	}

	data := make(map[string]any)
	data["name"] = name
	data["displayname"] = getDisplayName(name)
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

	name := uniqueName(r)

	if h.isIgnored(name) {
		h.Write404(w)
		return
	}
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")

	name = filepath.Clean(name)
	path := filepath.Join(h.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		h.Write404(w)
		return
	}

	contents, err := gr.FileContent(treePath)
	if err != nil {
		h.Write500(w)
		return
	}
	data := make(map[string]any)
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["desc"] = getDescription(path)
	data["path"] = treePath

	safe := sanitize([]byte(contents))

	if raw {
		h.showRaw(string(safe), w)
	} else {
		if h.c.Meta.SyntaxHighlight == "" {
			h.showFile(string(safe), data, w)
		} else {
			h.showFileWithHighlight(treePath, string(safe), data, w)
		}
	}
}

func (h *Handle) Archive(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if h.isIgnored(name) {
		h.Write404(w)
		return
	}

	file := chi.URLParam(r, "file")

	// TODO: extend this to add more files compression (e.g.: xz)
	if !strings.HasSuffix(file, ".tar.gz") {
		h.Write404(w)
		return
	}

	ref := strings.TrimSuffix(file, ".tar.gz")

	// This allows the browser to use a proper name for the file when
	// downloading
	filename := fmt.Sprintf("%s-%s.tar.gz", name, ref)
	setContentDisposition(w, filename)
	setGZipMIME(w)

	path := filepath.Join(h.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		h.Write404(w)
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
	name := uniqueName(r)
	if h.isIgnored(name) {
		h.Write404(w)
		return
	}
	ref := chi.URLParam(r, "ref")

	path := filepath.Join(h.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		h.Write404(w)
		return
	}

	commits, err := gr.Commits()
	if err != nil {
		h.Write500(w)
		log.Println(err)
		return
	}

	data := make(map[string]interface{})
	data["commits"] = commits
	data["meta"] = h.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["desc"] = getDescription(path)
	data["log"] = true

	if err := h.t.ExecuteTemplate(w, "repo/log", data); err != nil {
		log.Println(err)
		return
	}
}

func (h *Handle) Diff(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if h.isIgnored(name) {
		h.Write404(w)
		return
	}
	ref := chi.URLParam(r, "ref")

	path := filepath.Join(h.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		h.Write404(w)
		return
	}

	diff, err := gr.Diff()
	if err != nil {
		h.Write500(w)
		log.Println(err)
		return
	}

	data := make(map[string]interface{})

	data["commit"] = diff.Commit
	data["stat"] = diff.Stat
	data["diff"] = diff.Diff
	data["meta"] = h.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["desc"] = getDescription(path)

	if err := h.t.ExecuteTemplate(w, "repo/commit", data); err != nil {
		log.Println(err)
		return
	}
}

func (h *Handle) Refs(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if h.isIgnored(name) {
		h.Write404(w)
		return
	}

	path := filepath.Join(h.c.Repo.ScanPath, name)
	gr, err := git.Open(path, "")
	if err != nil {
		h.Write404(w)
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
		h.Write500(w)
		return
	}

	data := make(map[string]interface{})

	data["meta"] = h.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["branches"] = branches
	data["tags"] = tags
	data["desc"] = getDescription(path)

	if err := h.t.ExecuteTemplate(w, "repo/refs", data); err != nil {
		log.Println(err)
		return
	}
}

func (h *Handle) ServeStatic(w http.ResponseWriter, r *http.Request) {
	f := chi.URLParam(r, "file")
	f = filepath.Clean(filepath.Join(h.c.Dirs.Static, f))

	http.ServeFile(w, r, f)
}

func resolveIdent(ctx context.Context, arg string) (*identity.Identity, error) {
	id, err := syntax.ParseAtIdentifier(arg)
	if err != nil {
		return nil, err
	}

	dir := identity.DefaultDirectory()
	return dir.Lookup(ctx, *id)
}

func (h *Handle) Login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if err := h.t.ExecuteTemplate(w, "user/login", nil); err != nil {
			log.Println(err)
			return
		}
	case http.MethodPost:
		ctx := r.Context()
		username := r.FormValue("username")
		appPassword := r.FormValue("app_password")

		resolved, err := resolveIdent(ctx, username)
		if err != nil {
			http.Error(w, "invalid `handle`", http.StatusBadRequest)
			return
		}

		pdsUrl := resolved.PDSEndpoint()
		client := xrpc.Client{
			Host: pdsUrl,
		}

		atSession, err := comatproto.ServerCreateSession(ctx, &client, &comatproto.ServerCreateSession_Input{
			Identifier: resolved.DID.String(),
			Password:   appPassword,
		})

		clientSession, _ := h.s.Get(r, "bild-session")
		clientSession.Values["handle"] = atSession.Handle
		clientSession.Values["did"] = atSession.Did
		clientSession.Values["accessJwt"] = atSession.AccessJwt
		clientSession.Values["refreshJwt"] = atSession.RefreshJwt
		clientSession.Values["expiry"] = time.Now().Add(time.Hour).String()
		clientSession.Values["pds"] = pdsUrl
		clientSession.Values["authenticated"] = true

		err = clientSession.Save(r, w)

		if err != nil {
			log.Printf("failed to store session for did: %s\n", atSession.Did)
			log.Println(err)
			return
		}

		log.Printf("successfully saved session for %s (%s)", atSession.Handle, atSession.Did)
		http.Redirect(w, r, "/@"+atSession.Handle, 302)
	}
}

func (h *Handle) Keys(w http.ResponseWriter, r *http.Request) {
	session, _ := h.s.Get(r, "bild-session")
	did := session.Values["did"].(string)

	switch r.Method {
	case http.MethodGet:
		keys, err := h.db.GetPublicKeys(did)
		if err != nil {
			log.Println(err)
			http.Error(w, "invalid `did`", http.StatusBadRequest)
			return
		}

		data := make(map[string]interface{})
		data["keys"] = keys
		if err := h.t.ExecuteTemplate(w, "settings/keys", data); err != nil {
			log.Println(err)
			return
		}
	case http.MethodPut:
		key := r.FormValue("key")
		name := r.FormValue("name")

		_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
		if err != nil {
			h.WriteOOBNotice(w, "keys", "Invalid public key. Check your formatting and try again.")
			log.Printf("parsing public key: %s", err)
			return
		}

		if err := h.db.AddPublicKey(did, name, key); err != nil {
			h.WriteOOBNotice(w, "keys", "Failed to add key.")
			log.Printf("adding public key: %s", err)
			return
		}

		h.WriteOOBNotice(w, "keys", "Key added!")
		return
	}
}

func (h *Handle) NewRepo(w http.ResponseWriter, r *http.Request) {
	session, _ := h.s.Get(r, "bild-session")
	did := session.Values["did"].(string)

	switch r.Method {
	case http.MethodGet:
		if err := h.t.ExecuteTemplate(w, "repo/new", nil); err != nil {
			log.Println(err)
			return
		}
	case http.MethodPut:
		name := r.FormValue("name")
		description := r.FormValue("description")

		err := git.InitBare(filepath.Join(h.c.Repo.ScanPath, "example.com", name))
		if err != nil {
			h.WriteOOBNotice(w, "repo", "Error creating repo. Try again later.")
			return
		}

		err = h.db.AddRepo(did, name, description)
		if err != nil {
			h.WriteOOBNotice(w, "repo", "Error creating repo. Try again later.")
			return
		}

		w.Header().Set("HX-Redirect", fmt.Sprintf("/@example.com/%s", name))
		w.WriteHeader(http.StatusOK)
	}
}
