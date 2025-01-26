package routes

import (
	"compress/gzip"
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

	"github.com/dustin/go-humanize"
	"github.com/go-chi/chi/v5"
	"github.com/icyphox/bild/legit/config"
	"github.com/icyphox/bild/legit/git"
	"github.com/russross/blackfriday/v2"
)

type deps struct {
	c *config.Config
	t *template.Template
}

func (d *deps) Index(w http.ResponseWriter, r *http.Request) {
	user := chi.URLParam(r, "user")
	path := filepath.Join(d.c.Repo.ScanPath, user)
	dirs, err := os.ReadDir(path)
	if err != nil {
		d.Write500(w)
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
		if !dir.IsDir() || d.isIgnored(name) || d.isUnlisted(name) {
			continue
		}

		gr, err := git.Open(path, "")
		if err != nil {
			log.Println(err)
			continue
		}

		c, err := gr.LastCommit()
		if err != nil {
			d.Write500(w)
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
	data["meta"] = d.c.Meta
	data["info"] = infos

	if err := d.t.ExecuteTemplate(w, "index", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) RepoIndex(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}

	name = filepath.Clean(name)
	path := filepath.Join(d.c.Repo.ScanPath, name)

	fmt.Println(path)
	gr, err := git.Open(path, "")
	if err != nil {
		d.Write404(w)
		return
	}
	commits, err := gr.Commits()
	if err != nil {
		d.Write500(w)
		log.Println(err)
		return
	}

	var readmeContent template.HTML
	for _, readme := range d.c.Repo.Readme {
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

	mainBranch, err := gr.FindMainBranch(d.c.Repo.MainBranch)
	if err != nil {
		d.Write500(w)
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
	data["servername"] = d.c.Server.Name
	data["meta"] = d.c.Meta
	data["gomod"] = isGoModule(gr)

	if err := d.t.ExecuteTemplate(w, "repo", data); err != nil {
		log.Println(err)
		return
	}

	return
}

func (d *deps) RepoTree(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")

	name = filepath.Clean(name)
	path := filepath.Join(d.c.Repo.ScanPath, name)
	fmt.Println(path)
	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	files, err := gr.FileTree(treePath)
	if err != nil {
		d.Write500(w)
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

	d.listFiles(files, data, w)
	return
}

func (d *deps) FileContent(w http.ResponseWriter, r *http.Request) {
	var raw bool
	if rawParam, err := strconv.ParseBool(r.URL.Query().Get("raw")); err == nil {
		raw = rawParam
	}

	name := uniqueName(r)

	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	treePath := chi.URLParam(r, "*")
	ref := chi.URLParam(r, "ref")

	name = filepath.Clean(name)
	path := filepath.Join(d.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	contents, err := gr.FileContent(treePath)
	if err != nil {
		d.Write500(w)
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
		d.showRaw(string(safe), w)
	} else {
		if d.c.Meta.SyntaxHighlight == "" {
			d.showFile(string(safe), data, w)
		} else {
			d.showFileWithHighlight(treePath, string(safe), data, w)
		}
	}
}

func (d *deps) Archive(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}

	file := chi.URLParam(r, "file")

	// TODO: extend this to add more files compression (e.g.: xz)
	if !strings.HasSuffix(file, ".tar.gz") {
		d.Write404(w)
		return
	}

	ref := strings.TrimSuffix(file, ".tar.gz")

	// This allows the browser to use a proper name for the file when
	// downloading
	filename := fmt.Sprintf("%s-%s.tar.gz", name, ref)
	setContentDisposition(w, filename)
	setGZipMIME(w)

	path := filepath.Join(d.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
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

func (d *deps) Log(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	ref := chi.URLParam(r, "ref")

	path := filepath.Join(d.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	commits, err := gr.Commits()
	if err != nil {
		d.Write500(w)
		log.Println(err)
		return
	}

	data := make(map[string]interface{})
	data["commits"] = commits
	data["meta"] = d.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["desc"] = getDescription(path)
	data["log"] = true

	if err := d.t.ExecuteTemplate(w, "log", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) Diff(w http.ResponseWriter, r *http.Request) {
	name := uniqueName(r)
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	ref := chi.URLParam(r, "ref")

	path := filepath.Join(d.c.Repo.ScanPath, name)
	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	diff, err := gr.Diff()
	if err != nil {
		d.Write500(w)
		log.Println(err)
		return
	}

	data := make(map[string]interface{})

	data["commit"] = diff.Commit
	data["stat"] = diff.Stat
	data["diff"] = diff.Diff
	data["meta"] = d.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["desc"] = getDescription(path)

	if err := d.t.ExecuteTemplate(w, "commit", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) Refs(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}

	path := filepath.Join(d.c.Repo.ScanPath, name)
	gr, err := git.Open(path, "")
	if err != nil {
		d.Write404(w)
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
		d.Write500(w)
		return
	}

	data := make(map[string]interface{})

	data["meta"] = d.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["branches"] = branches
	data["tags"] = tags
	data["desc"] = getDescription(path)

	if err := d.t.ExecuteTemplate(w, "refs", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) ServeStatic(w http.ResponseWriter, r *http.Request) {
	f := chi.URLParam(r, "file")
	f = filepath.Clean(filepath.Join(d.c.Dirs.Static, f))

	http.ServeFile(w, r, f)
}

func (d *deps) Login(w http.ResponseWriter, r *http.Request) {
	if err := d.t.ExecuteTemplate(w, "login", nil); err != nil {
		log.Println(err)
		return
	}
}
