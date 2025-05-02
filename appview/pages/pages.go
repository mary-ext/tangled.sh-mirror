package pages

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"tangled.sh/tangled.sh/core/appview/auth"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/appview/pages/repoinfo"
	"tangled.sh/tangled.sh/core/appview/pagination"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/types"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microcosm-cc/bluemonday"
)

//go:embed templates/* static
var Files embed.FS

type Pages struct {
	t           map[string]*template.Template
	dev         bool
	embedFS     embed.FS
	templateDir string // Path to templates on disk for dev mode
	rctx        *markup.RenderContext
}

func NewPages(dev bool) *Pages {
	// initialized with safe defaults, can be overriden per use
	rctx := &markup.RenderContext{
		IsDev: dev,
	}

	p := &Pages{
		t:           make(map[string]*template.Template),
		dev:         dev,
		embedFS:     Files,
		rctx:        rctx,
		templateDir: "appview/pages",
	}

	// Initial load of all templates
	p.loadAllTemplates()

	return p
}

func (p *Pages) loadAllTemplates() {
	templates := make(map[string]*template.Template)
	var fragmentPaths []string

	// Use embedded FS for initial loading
	// First, collect all fragment paths
	err := fs.WalkDir(p.embedFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") {
			return nil
		}
		if !strings.Contains(path, "fragments/") {
			return nil
		}
		name := strings.TrimPrefix(path, "templates/")
		name = strings.TrimSuffix(name, ".html")
		tmpl, err := template.New(name).
			Funcs(funcMap()).
			ParseFS(p.embedFS, path)
		if err != nil {
			log.Fatalf("setting up fragment: %v", err)
		}
		templates[name] = tmpl
		fragmentPaths = append(fragmentPaths, path)
		log.Printf("loaded fragment: %s", name)
		return nil
	})
	if err != nil {
		log.Fatalf("walking template dir for fragments: %v", err)
	}

	// Then walk through and setup the rest of the templates
	err = fs.WalkDir(p.embedFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, "html") {
			return nil
		}
		// Skip fragments as they've already been loaded
		if strings.Contains(path, "fragments/") {
			return nil
		}
		// Skip layouts
		if strings.Contains(path, "layouts/") {
			return nil
		}
		name := strings.TrimPrefix(path, "templates/")
		name = strings.TrimSuffix(name, ".html")
		// Add the page template on top of the base
		allPaths := []string{}
		allPaths = append(allPaths, "templates/layouts/*.html")
		allPaths = append(allPaths, fragmentPaths...)
		allPaths = append(allPaths, path)
		tmpl, err := template.New(name).
			Funcs(funcMap()).
			ParseFS(p.embedFS, allPaths...)
		if err != nil {
			return fmt.Errorf("setting up template: %w", err)
		}
		templates[name] = tmpl
		log.Printf("loaded template: %s", name)
		return nil
	})
	if err != nil {
		log.Fatalf("walking template dir: %v", err)
	}

	log.Printf("total templates loaded: %d", len(templates))
	p.t = templates
}

// loadTemplateFromDisk loads a template from the filesystem in dev mode
func (p *Pages) loadTemplateFromDisk(name string) error {
	if !p.dev {
		return nil
	}

	log.Printf("reloading template from disk: %s", name)

	// Find all fragments first
	var fragmentPaths []string
	err := filepath.WalkDir(filepath.Join(p.templateDir, "templates"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") {
			return nil
		}
		if !strings.Contains(path, "fragments/") {
			return nil
		}
		fragmentPaths = append(fragmentPaths, path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking disk template dir for fragments: %w", err)
	}

	// Find the template path on disk
	templatePath := filepath.Join(p.templateDir, "templates", name+".html")
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		return fmt.Errorf("template not found on disk: %s", name)
	}

	// Create a new template
	tmpl := template.New(name).Funcs(funcMap())

	// Parse layouts
	layoutGlob := filepath.Join(p.templateDir, "templates", "layouts", "*.html")
	layouts, err := filepath.Glob(layoutGlob)
	if err != nil {
		return fmt.Errorf("finding layout templates: %w", err)
	}

	// Create paths for parsing
	allFiles := append(layouts, fragmentPaths...)
	allFiles = append(allFiles, templatePath)

	// Parse all templates
	tmpl, err = tmpl.ParseFiles(allFiles...)
	if err != nil {
		return fmt.Errorf("parsing template files: %w", err)
	}

	// Update the template in the map
	p.t[name] = tmpl
	log.Printf("template reloaded from disk: %s", name)
	return nil
}

func (p *Pages) executeOrReload(templateName string, w io.Writer, base string, params any) error {
	// In dev mode, reload the template from disk before executing
	if p.dev {
		if err := p.loadTemplateFromDisk(templateName); err != nil {
			log.Printf("warning: failed to reload template %s from disk: %v", templateName, err)
			// Continue with the existing template
		}
	}

	tmpl, exists := p.t[templateName]
	if !exists {
		return fmt.Errorf("template not found: %s", templateName)
	}

	if base == "" {
		return tmpl.Execute(w, params)
	} else {
		return tmpl.ExecuteTemplate(w, base, params)
	}
}

func (p *Pages) execute(name string, w io.Writer, params any) error {
	return p.executeOrReload(name, w, "layouts/base", params)
}

func (p *Pages) executePlain(name string, w io.Writer, params any) error {
	return p.executeOrReload(name, w, "", params)
}

func (p *Pages) executeRepo(name string, w io.Writer, params any) error {
	return p.executeOrReload(name, w, "layouts/repobase", params)
}

type LoginParams struct {
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
	Emails       []db.Email
}

func (p *Pages) Settings(w io.Writer, params SettingsParams) error {
	return p.execute("settings", w, params)
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
	DidHandleMap map[string]string
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

type ForkRepoParams struct {
	LoggedInUser *auth.User
	Knots        []string
	RepoInfo     repoinfo.RepoInfo
}

func (p *Pages) ForkRepo(w io.Writer, params ForkRepoParams) error {
	return p.execute("repo/fork", w, params)
}

type ProfilePageParams struct {
	LoggedInUser       *auth.User
	UserDid            string
	UserHandle         string
	Repos              []db.Repo
	CollaboratingRepos []db.Repo
	ProfileStats       ProfileStats
	FollowStatus       db.FollowStatus
	AvatarUri          string
	ProfileTimeline    *db.ProfileTimeline

	DidHandleMap map[string]string
}

type ProfileStats struct {
	Followers int
	Following int
}

func (p *Pages) ProfilePage(w io.Writer, params ProfilePageParams) error {
	return p.execute("user/profile", w, params)
}

type FollowFragmentParams struct {
	UserDid      string
	FollowStatus db.FollowStatus
}

func (p *Pages) FollowFragment(w io.Writer, params FollowFragmentParams) error {
	return p.executePlain("user/fragments/follow", w, params)
}

type RepoActionsFragmentParams struct {
	IsStarred bool
	RepoAt    syntax.ATURI
	Stats     db.RepoStats
}

func (p *Pages) RepoActionsFragment(w io.Writer, params RepoActionsFragmentParams) error {
	return p.executePlain("repo/fragments/repoActions", w, params)
}

type RepoDescriptionParams struct {
	RepoInfo repoinfo.RepoInfo
}

func (p *Pages) EditRepoDescriptionFragment(w io.Writer, params RepoDescriptionParams) error {
	return p.executePlain("repo/fragments/editRepoDescription", w, params)
}

func (p *Pages) RepoDescriptionFragment(w io.Writer, params RepoDescriptionParams) error {
	return p.executePlain("repo/fragments/repoDescription", w, params)
}

type RepoIndexParams struct {
	LoggedInUser  *auth.User
	RepoInfo      repoinfo.RepoInfo
	Active        string
	TagMap        map[string][]string
	CommitsTrunc  []*object.Commit
	TagsTrunc     []*types.TagReference
	BranchesTrunc []types.Branch
	types.RepoIndexResponse
	HTMLReadme         template.HTML
	Raw                bool
	EmailToDidOrHandle map[string]string
}

func (p *Pages) RepoIndexPage(w io.Writer, params RepoIndexParams) error {
	params.Active = "overview"
	if params.IsEmpty {
		return p.executeRepo("repo/empty", w, params)
	}

	p.rctx = &markup.RenderContext{
		RepoInfo:     params.RepoInfo,
		IsDev:        p.dev,
		RendererType: markup.RendererTypeRepoMarkdown,
	}

	if params.ReadmeFileName != "" {
		var htmlString string
		ext := filepath.Ext(params.ReadmeFileName)
		switch ext {
		case ".md", ".markdown", ".mdown", ".mkdn", ".mkd":
			htmlString = p.rctx.RenderMarkdown(params.Readme)
			params.Raw = false
			params.HTMLReadme = template.HTML(bluemonday.UGCPolicy().Sanitize(htmlString))
		default:
			htmlString = string(params.Readme)
			params.Raw = true
			params.HTMLReadme = template.HTML(bluemonday.NewPolicy().Sanitize(htmlString))
		}
	}

	return p.executeRepo("repo/index", w, params)
}

type RepoLogParams struct {
	LoggedInUser *auth.User
	RepoInfo     repoinfo.RepoInfo
	TagMap       map[string][]string
	types.RepoLogResponse
	Active             string
	EmailToDidOrHandle map[string]string
}

func (p *Pages) RepoLog(w io.Writer, params RepoLogParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/log", w, params)
}

type RepoCommitParams struct {
	LoggedInUser       *auth.User
	RepoInfo           repoinfo.RepoInfo
	Active             string
	EmailToDidOrHandle map[string]string

	types.RepoCommitResponse
}

func (p *Pages) RepoCommit(w io.Writer, params RepoCommitParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/commit", w, params)
}

type RepoTreeParams struct {
	LoggedInUser *auth.User
	RepoInfo     repoinfo.RepoInfo
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
	RepoInfo     repoinfo.RepoInfo
	Active       string
	types.RepoBranchesResponse
}

func (p *Pages) RepoBranches(w io.Writer, params RepoBranchesParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/branches", w, params)
}

type RepoTagsParams struct {
	LoggedInUser *auth.User
	RepoInfo     repoinfo.RepoInfo
	Active       string
	types.RepoTagsResponse
}

func (p *Pages) RepoTags(w io.Writer, params RepoTagsParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/tags", w, params)
}

type RepoBlobParams struct {
	LoggedInUser     *auth.User
	RepoInfo         repoinfo.RepoInfo
	Active           string
	BreadCrumbs      [][]string
	ShowRendered     bool
	RenderToggle     bool
	RenderedContents template.HTML
	types.RepoBlobResponse
}

func (p *Pages) RepoBlob(w io.Writer, params RepoBlobParams) error {
	var style *chroma.Style = styles.Get("catpuccin-latte")

	if params.ShowRendered {
		switch markup.GetFormat(params.Path) {
		case markup.FormatMarkdown:
			p.rctx = &markup.RenderContext{
				RepoInfo:     params.RepoInfo,
				IsDev:        p.dev,
				RendererType: markup.RendererTypeRepoMarkdown,
			}
			params.RenderedContents = template.HTML(p.rctx.RenderMarkdown(params.Contents))
		}
	}

	if params.Lines < 5000 {
		c := params.Contents
		formatter := chromahtml.New(
			chromahtml.InlineCode(false),
			chromahtml.WithLineNumbers(true),
			chromahtml.WithLinkableLineNumbers(true, "L"),
			chromahtml.Standalone(false),
			chromahtml.WithClasses(true),
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
	LoggedInUser  *auth.User
	RepoInfo      repoinfo.RepoInfo
	Collaborators []Collaborator
	Active        string
	Branches      []string
	DefaultBranch string
	// TODO: use repoinfo.roles
	IsCollaboratorInviteAllowed bool
}

func (p *Pages) RepoSettings(w io.Writer, params RepoSettingsParams) error {
	params.Active = "settings"
	return p.executeRepo("repo/settings", w, params)
}

type RepoIssuesParams struct {
	LoggedInUser    *auth.User
	RepoInfo        repoinfo.RepoInfo
	Active          string
	Issues          []db.Issue
	DidHandleMap    map[string]string
	Page            pagination.Page
	FilteringByOpen bool
}

func (p *Pages) RepoIssues(w io.Writer, params RepoIssuesParams) error {
	params.Active = "issues"
	return p.executeRepo("repo/issues/issues", w, params)
}

type RepoSingleIssueParams struct {
	LoggedInUser     *auth.User
	RepoInfo         repoinfo.RepoInfo
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
	RepoInfo     repoinfo.RepoInfo
	Active       string
}

func (p *Pages) RepoNewIssue(w io.Writer, params RepoNewIssueParams) error {
	params.Active = "issues"
	return p.executeRepo("repo/issues/new", w, params)
}

type EditIssueCommentParams struct {
	LoggedInUser *auth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *db.Issue
	Comment      *db.Comment
}

func (p *Pages) EditIssueCommentFragment(w io.Writer, params EditIssueCommentParams) error {
	return p.executePlain("repo/issues/fragments/editIssueComment", w, params)
}

type SingleIssueCommentParams struct {
	LoggedInUser *auth.User
	DidHandleMap map[string]string
	RepoInfo     repoinfo.RepoInfo
	Issue        *db.Issue
	Comment      *db.Comment
}

func (p *Pages) SingleIssueCommentFragment(w io.Writer, params SingleIssueCommentParams) error {
	return p.executePlain("repo/issues/fragments/issueComment", w, params)
}

type RepoNewPullParams struct {
	LoggedInUser *auth.User
	RepoInfo     repoinfo.RepoInfo
	Branches     []types.Branch
	Active       string
}

func (p *Pages) RepoNewPull(w io.Writer, params RepoNewPullParams) error {
	params.Active = "pulls"
	return p.executeRepo("repo/pulls/new", w, params)
}

type RepoPullsParams struct {
	LoggedInUser *auth.User
	RepoInfo     repoinfo.RepoInfo
	Pulls        []*db.Pull
	Active       string
	DidHandleMap map[string]string
	FilteringBy  db.PullState
}

func (p *Pages) RepoPulls(w io.Writer, params RepoPullsParams) error {
	params.Active = "pulls"
	return p.executeRepo("repo/pulls/pulls", w, params)
}

type ResubmitResult uint64

const (
	ShouldResubmit ResubmitResult = iota
	ShouldNotResubmit
	Unknown
)

func (r ResubmitResult) Yes() bool {
	return r == ShouldResubmit
}
func (r ResubmitResult) No() bool {
	return r == ShouldNotResubmit
}
func (r ResubmitResult) Unknown() bool {
	return r == Unknown
}

type RepoSinglePullParams struct {
	LoggedInUser  *auth.User
	RepoInfo      repoinfo.RepoInfo
	Active        string
	DidHandleMap  map[string]string
	Pull          *db.Pull
	MergeCheck    types.MergeCheckResponse
	ResubmitCheck ResubmitResult
}

func (p *Pages) RepoSinglePull(w io.Writer, params RepoSinglePullParams) error {
	params.Active = "pulls"
	return p.executeRepo("repo/pulls/pull", w, params)
}

type RepoPullPatchParams struct {
	LoggedInUser *auth.User
	DidHandleMap map[string]string
	RepoInfo     repoinfo.RepoInfo
	Pull         *db.Pull
	Diff         *types.NiceDiff
	Round        int
	Submission   *db.PullSubmission
}

// this name is a mouthful
func (p *Pages) RepoPullPatchPage(w io.Writer, params RepoPullPatchParams) error {
	return p.execute("repo/pulls/patch", w, params)
}

type RepoPullInterdiffParams struct {
	LoggedInUser *auth.User
	DidHandleMap map[string]string
	RepoInfo     repoinfo.RepoInfo
	Pull         *db.Pull
	Round        int
	Interdiff    *patchutil.InterdiffResult
}

// this name is a mouthful
func (p *Pages) RepoPullInterdiffPage(w io.Writer, params RepoPullInterdiffParams) error {
	return p.execute("repo/pulls/interdiff", w, params)
}

type PullPatchUploadParams struct {
	RepoInfo repoinfo.RepoInfo
}

func (p *Pages) PullPatchUploadFragment(w io.Writer, params PullPatchUploadParams) error {
	return p.executePlain("repo/pulls/fragments/pullPatchUpload", w, params)
}

type PullCompareBranchesParams struct {
	RepoInfo repoinfo.RepoInfo
	Branches []types.Branch
}

func (p *Pages) PullCompareBranchesFragment(w io.Writer, params PullCompareBranchesParams) error {
	return p.executePlain("repo/pulls/fragments/pullCompareBranches", w, params)
}

type PullCompareForkParams struct {
	RepoInfo repoinfo.RepoInfo
	Forks    []db.Repo
}

func (p *Pages) PullCompareForkFragment(w io.Writer, params PullCompareForkParams) error {
	return p.executePlain("repo/pulls/fragments/pullCompareForks", w, params)
}

type PullCompareForkBranchesParams struct {
	RepoInfo       repoinfo.RepoInfo
	SourceBranches []types.Branch
	TargetBranches []types.Branch
}

func (p *Pages) PullCompareForkBranchesFragment(w io.Writer, params PullCompareForkBranchesParams) error {
	return p.executePlain("repo/pulls/fragments/pullCompareForksBranches", w, params)
}

type PullResubmitParams struct {
	LoggedInUser *auth.User
	RepoInfo     repoinfo.RepoInfo
	Pull         *db.Pull
	SubmissionId int
}

func (p *Pages) PullResubmitFragment(w io.Writer, params PullResubmitParams) error {
	return p.executePlain("repo/pulls/fragments/pullResubmit", w, params)
}

type PullActionsParams struct {
	LoggedInUser  *auth.User
	RepoInfo      repoinfo.RepoInfo
	Pull          *db.Pull
	RoundNumber   int
	MergeCheck    types.MergeCheckResponse
	ResubmitCheck ResubmitResult
}

func (p *Pages) PullActionsFragment(w io.Writer, params PullActionsParams) error {
	return p.executePlain("repo/pulls/fragments/pullActions", w, params)
}

type PullNewCommentParams struct {
	LoggedInUser *auth.User
	RepoInfo     repoinfo.RepoInfo
	Pull         *db.Pull
	RoundNumber  int
}

func (p *Pages) PullNewCommentFragment(w io.Writer, params PullNewCommentParams) error {
	return p.executePlain("repo/pulls/fragments/pullNewComment", w, params)
}

func (p *Pages) Static() http.Handler {
	if p.dev {
		return http.StripPrefix("/static/", http.FileServer(http.Dir("appview/pages/static")))
	}

	sub, err := fs.Sub(Files, "static")
	if err != nil {
		log.Fatalf("no static dir found? that's crazy: %v", err)
	}
	// Custom handler to apply Cache-Control headers for font files
	return Cache(http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
}

func Cache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Split(r.URL.Path, "?")[0]

		if strings.HasSuffix(path, ".css") {
			// on day for css files
			w.Header().Set("Cache-Control", "public, max-age=86400")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		h.ServeHTTP(w, r)
	})
}

func CssContentHash() string {
	cssFile, err := Files.Open("static/tw.css")
	if err != nil {
		log.Printf("Error opening CSS file: %v", err)
		return ""
	}
	defer cssFile.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, cssFile); err != nil {
		log.Printf("Error hashing CSS file: %v", err)
		return ""
	}

	return hex.EncodeToString(hasher.Sum(nil))[:8] // Use first 8 chars of hash
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
