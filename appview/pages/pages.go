package pages

import (
	"bytes"
	"embed"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/dustin/go-humanize"
	"github.com/sotangled/tangled/appview/auth"
	"github.com/sotangled/tangled/appview/db"
	"github.com/sotangled/tangled/types"
)

//go:embed templates/* static/*
var files embed.FS

type Pages struct {
	t map[string]*template.Template
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"split": func(s string) []string {
			return strings.Split(s, "\n")
		},
		"splitOn": func(s, sep string) []string {
			return strings.Split(s, sep)
		},
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"cond": func(cond interface{}, a, b string) string {
			if cond == nil {
				return b
			}

			if boolean, ok := cond.(bool); boolean && ok {
				return a
			}

			return b
		},
		"didOrHandle": func(did, handle string) string {
			if handle != "" {
				return fmt.Sprintf("@%s", handle)
			} else {
				return did
			}
		},
		"assoc": func(values ...string) ([][]string, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid assoc call, must have an even number of arguments")
			}
			pairs := make([][]string, 0)
			for i := 0; i < len(values); i += 2 {
				pairs = append(pairs, []string{values[i], values[i+1]})
			}
			return pairs, nil
		},
		"append": func(s []string, values ...string) []string {
			s = append(s, values...)
			return s
		},
		"timeFmt": humanize.Time,
		"byteFmt": humanize.Bytes,
		"length": func(v []string) int {
			return len(v)
		},
		"splitN": func(s, sep string, n int) []string {
			return strings.SplitN(s, sep, n)
		},
		"escapeHtml": func(s string) template.HTML {
			if s == "" {
				return template.HTML("<br>")
			}
			return template.HTML(s)
		},
		"unescapeHtml": func(s string) string {
			return html.UnescapeString(s)
		},
		"nl2br": func(text string) template.HTML {
			return template.HTML(strings.Replace(template.HTMLEscapeString(text), "\n", "<br>", -1))
		},
		"unwrapText": func(text string) string {
			paragraphs := strings.Split(text, "\n\n")

			for i, p := range paragraphs {
				lines := strings.Split(p, "\n")
				paragraphs[i] = strings.Join(lines, " ")
			}

			return strings.Join(paragraphs, "\n\n")
		},
		"sequence": func(n int) []struct{} {
			return make([]struct{}, n)
		},
	}
}

func NewPages() *Pages {
	templates := make(map[string]*template.Template)

	// Walk through embedded templates directory and parse all .html files
	err := fs.WalkDir(files, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			name := strings.TrimPrefix(path, "templates/")
			name = strings.TrimSuffix(name, ".html")

			if !strings.HasPrefix(path, "templates/layouts/") {
				// Add the page template on top of the base
				tmpl, err := template.New(name).
					Funcs(funcMap()).
					ParseFS(files, "templates/layouts/*.html", path)
				if err != nil {
					return fmt.Errorf("setting up template: %w", err)
				}

				templates[name] = tmpl
				log.Printf("loaded template: %s", name)
			}

			return nil
		}
		return nil
	})
	if err != nil {
		log.Fatalf("walking template dir: %v", err)
	}

	log.Printf("total templates loaded: %d", len(templates))

	return &Pages{
		t: templates,
	}
}

type LoginParams struct {
}

func (p *Pages) execute(name string, w io.Writer, params any) error {
	return p.t[name].ExecuteTemplate(w, "layouts/base", params)
}

func (p *Pages) executePlain(name string, w io.Writer, params any) error {
	return p.t[name].Execute(w, params)
}

func (p *Pages) executeRepo(name string, w io.Writer, params any) error {
	return p.t[name].ExecuteTemplate(w, "layouts/repobase", params)
}

func (p *Pages) Login(w io.Writer, params LoginParams) error {
	return p.executePlain("user/login", w, params)
}

type TimelineParams struct {
	LoggedInUser *auth.User
	Timeline     []db.TimelineEvent
	DidHandleMap map[string]string
}

func (p *Pages) Timeline(w io.Writer, params TimelineParams) error {
	return p.execute("timeline", w, params)
}

type SettingsParams struct {
	LoggedInUser *auth.User
	PubKeys      []db.PublicKey
}

func (p *Pages) Settings(w io.Writer, params SettingsParams) error {
	return p.execute("settings/keys", w, params)
}

type KnotsParams struct {
	LoggedInUser  *auth.User
	Registrations []db.Registration
}

func (p *Pages) Knots(w io.Writer, params KnotsParams) error {
	return p.execute("knots", w, params)
}

type KnotParams struct {
	LoggedInUser *auth.User
	Registration *db.Registration
	Members      []string
	IsOwner      bool
}

func (p *Pages) Knot(w io.Writer, params KnotParams) error {
	return p.execute("knot", w, params)
}

type NewRepoParams struct {
	LoggedInUser *auth.User
	Knots        []string
}

func (p *Pages) NewRepo(w io.Writer, params NewRepoParams) error {
	return p.execute("repo/new", w, params)
}

type ProfilePageParams struct {
	LoggedInUser       *auth.User
	UserDid            string
	UserHandle         string
	Repos              []db.Repo
	CollaboratingRepos []db.Repo
	ProfileStats       ProfileStats
	FollowStatus       db.FollowStatus
}

type ProfileStats struct {
	Followers int
	Following int
}

func (p *Pages) ProfilePage(w io.Writer, params ProfilePageParams) error {
	return p.execute("user/profile", w, params)
}

type RepoInfo struct {
	Name            string
	OwnerDid        string
	OwnerHandle     string
	Description     string
	SettingsAllowed bool
}

func (r RepoInfo) OwnerWithAt() string {
	if r.OwnerHandle != "" {
		return fmt.Sprintf("@%s", r.OwnerHandle)
	} else {
		return r.OwnerDid
	}
}

func (r RepoInfo) FullName() string {
	return path.Join(r.OwnerWithAt(), r.Name)
}

func (r RepoInfo) GetTabs() [][]string {
	tabs := [][]string{
		{"overview", "/"},
		{"issues", "/issues"},
		{"pulls", "/pulls"},
	}

	if r.SettingsAllowed {
		tabs = append(tabs, []string{"settings", "/settings"})
	}

	return tabs
}

type RepoIndexParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Active       string
	types.RepoIndexResponse
}

func (p *Pages) RepoIndexPage(w io.Writer, params RepoIndexParams) error {
	params.Active = "overview"
	if params.IsEmpty {
		return p.executeRepo("repo/empty", w, params)
	}
	return p.executeRepo("repo/index", w, params)
}

type RepoLogParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	types.RepoLogResponse
	Active string
}

func (p *Pages) RepoLog(w io.Writer, params RepoLogParams) error {
	params.Active = "overview"
	return p.execute("repo/log", w, params)
}

type RepoCommitParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Active       string
	types.RepoCommitResponse
}

func (p *Pages) RepoCommit(w io.Writer, params RepoCommitParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/commit", w, params)
}

type RepoTreeParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Active       string
	BreadCrumbs  [][]string
	BaseTreeLink string
	BaseBlobLink string
	types.RepoTreeResponse
}

type RepoTreeStats struct {
	NumFolders uint64
	NumFiles   uint64
}

func (r RepoTreeParams) TreeStats() RepoTreeStats {
	numFolders, numFiles := 0, 0
	for _, f := range r.Files {
		if !f.IsFile {
			numFolders += 1
		} else if f.IsFile {
			numFiles += 1
		}
	}

	return RepoTreeStats{
		NumFolders: uint64(numFolders),
		NumFiles:   uint64(numFiles),
	}
}

func (p *Pages) RepoTree(w io.Writer, params RepoTreeParams) error {
	params.Active = "overview"
	return p.execute("repo/tree", w, params)
}

type RepoBranchesParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	types.RepoBranchesResponse
}

func (p *Pages) RepoBranches(w io.Writer, params RepoBranchesParams) error {
	return p.executeRepo("repo/branches", w, params)
}

type RepoTagsParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	types.RepoTagsResponse
}

func (p *Pages) RepoTags(w io.Writer, params RepoTagsParams) error {
	return p.executeRepo("repo/tags", w, params)
}

type RepoBlobParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Active       string
	BreadCrumbs  [][]string
	types.RepoBlobResponse
}

func (p *Pages) RepoBlob(w io.Writer, params RepoBlobParams) error {
	style := styles.Get("bw")
	b := style.Builder()
	b.Add(chroma.LiteralString, "noitalic")
	style, _ = b.Build()

	if params.Lines < 5000 {
		c := params.Contents
		formatter := chromahtml.New(
			chromahtml.InlineCode(true),
			chromahtml.WithLineNumbers(true),
			chromahtml.WithLinkableLineNumbers(true, "L"),
			chromahtml.Standalone(false),
		)

		lexer := lexers.Get(filepath.Base(params.Path))
		if lexer == nil {
			lexer = lexers.Fallback
		}

		iterator, err := lexer.Tokenise(nil, c)
		if err != nil {
			return fmt.Errorf("chroma tokenize: %w", err)
		}

		var code bytes.Buffer
		err = formatter.Format(&code, style, iterator)
		if err != nil {
			return fmt.Errorf("chroma format: %w", err)
		}

		params.Contents = code.String()
	}

	params.Active = "overview"
	return p.executeRepo("repo/blob", w, params)
}

type Collaborator struct {
	Did    string
	Handle string
	Role   string
}

type RepoSettingsParams struct {
	LoggedInUser                *auth.User
	RepoInfo                    RepoInfo
	Collaborators               []Collaborator
	Active                      string
	IsCollaboratorInviteAllowed bool
}

func (p *Pages) RepoSettings(w io.Writer, params RepoSettingsParams) error {
	params.Active = "settings"
	return p.executeRepo("repo/settings", w, params)
}

type RepoIssuesParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Active       string
	Issues       []db.Issue
	DidHandleMap map[string]string
}

func (p *Pages) RepoIssues(w io.Writer, params RepoIssuesParams) error {
	params.Active = "issues"
	return p.executeRepo("repo/issues/issues", w, params)
}

type RepoSingleIssueParams struct {
	LoggedInUser     *auth.User
	RepoInfo         RepoInfo
	Active           string
	Issue            db.Issue
	Comments         []db.Comment
	IssueOwnerHandle string
	DidHandleMap     map[string]string

	State string
}

func (p *Pages) RepoSingleIssue(w io.Writer, params RepoSingleIssueParams) error {
	params.Active = "issues"
	if params.Issue.Open {
		params.State = "open"
	} else {
		params.State = "closed"
	}
	return p.execute("repo/issues/issue", w, params)
}

type RepoNewIssueParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Active       string
}

func (p *Pages) RepoNewIssue(w io.Writer, params RepoNewIssueParams) error {
	params.Active = "issues"
	return p.executeRepo("repo/issues/new", w, params)
}

func (p *Pages) Static() http.Handler {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		log.Fatalf("no static dir found? that's crazy: %v", err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}

func (p *Pages) Error500(w io.Writer) error {
	return p.execute("errors/500", w, nil)
}

func (p *Pages) Error404(w io.Writer) error {
	return p.execute("errors/404", w, nil)
}

func (p *Pages) Error503(w io.Writer) error {
	return p.execute("errors/503", w, nil)
}
