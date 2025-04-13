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
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/microcosm-cc/bluemonday"
	"tangled.sh/tangled.sh/core/appview/auth"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/appview/state/userutil"
	"tangled.sh/tangled.sh/core/types"
)

//go:embed templates/* static
var Files embed.FS

type Pages struct {
	t map[string]*template.Template
}

func NewPages() *Pages {
	templates := make(map[string]*template.Template)

	var fragmentPaths []string
	// First, collect all fragment paths
	err := fs.WalkDir(Files, "templates", func(path string, d fs.DirEntry, err error) error {
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
			ParseFS(Files, path)
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
	err = fs.WalkDir(Files, "templates", func(path string, d fs.DirEntry, err error) error {
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
			ParseFS(Files, allPaths...)
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
	RepoInfo     RepoInfo
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
	RepoInfo RepoInfo
}

func (p *Pages) EditRepoDescriptionFragment(w io.Writer, params RepoDescriptionParams) error {
	return p.executePlain("repo/fragments/editRepoDescription", w, params)
}

func (p *Pages) RepoDescriptionFragment(w io.Writer, params RepoDescriptionParams) error {
	return p.executePlain("repo/fragments/repoDescription", w, params)
}

type RepoInfo struct {
	Name         string
	OwnerDid     string
	OwnerHandle  string
	Description  string
	Knot         string
	RepoAt       syntax.ATURI
	IsStarred    bool
	Stats        db.RepoStats
	Roles        RolesInRepo
	Source       *db.Repo
	SourceHandle string
	DisableFork  bool
}

type RolesInRepo struct {
	Roles []string
}

func (r RolesInRepo) SettingsAllowed() bool {
	return slices.Contains(r.Roles, "repo:settings")
}

func (r RolesInRepo) CollaboratorInviteAllowed() bool {
	return slices.Contains(r.Roles, "repo:invite")
}

func (r RolesInRepo) RepoDeleteAllowed() bool {
	return slices.Contains(r.Roles, "repo:delete")
}

func (r RolesInRepo) IsOwner() bool {
	return slices.Contains(r.Roles, "repo:owner")
}

func (r RolesInRepo) IsCollaborator() bool {
	return slices.Contains(r.Roles, "repo:collaborator")
}

func (r RolesInRepo) IsPushAllowed() bool {
	return slices.Contains(r.Roles, "repo:push")
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

func (r RepoInfo) OwnerWithoutAt() string {
	if strings.HasPrefix(r.OwnerWithAt(), "@") {
		return strings.TrimPrefix(r.OwnerWithAt(), "@")
	} else {
		return userutil.FlattenDid(r.OwnerDid)
	}
}

func (r RepoInfo) FullNameWithoutAt() string {
	return path.Join(r.OwnerWithoutAt(), r.Name)
}

func (r RepoInfo) GetTabs() [][]string {
	tabs := [][]string{
		{"overview", "/", "square-chart-gantt"},
		{"issues", "/issues", "circle-dot"},
		{"pulls", "/pulls", "git-pull-request"},
	}

	if r.Roles.SettingsAllowed() {
		tabs = append(tabs, []string{"settings", "/settings", "cog"})
	}

	return tabs
}

// each tab on a repo could have some metadata:
//
// issues -> number of open issues etc.
// settings -> a warning icon to setup branch protection? idk
//
// we gather these bits of info here, because go templates
// are difficult to program in
func (r RepoInfo) TabMetadata() map[string]any {
	meta := make(map[string]any)

	if r.Stats.PullCount.Open > 0 {
		meta["pulls"] = r.Stats.PullCount.Open
	}

	if r.Stats.IssueCount.Open > 0 {
		meta["issues"] = r.Stats.IssueCount.Open
	}

	// more stuff?

	return meta
}

type RepoIndexParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Active       string
	TagMap       map[string][]string
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

	if params.ReadmeFileName != "" {
		var htmlString string
		ext := filepath.Ext(params.ReadmeFileName)
		switch ext {
		case ".md", ".markdown", ".mdown", ".mkdn", ".mkd":
			htmlString = markup.RenderMarkdown(params.Readme)
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
	RepoInfo     RepoInfo
	types.RepoLogResponse
	Active             string
	EmailToDidOrHandle map[string]string
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
	EmailToDidOrHandle map[string]string
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
	LoggedInUser     *auth.User
	RepoInfo         RepoInfo
	Active           string
	BreadCrumbs      [][]string
	ShowRendered     bool
	RenderToggle     bool
	RenderedContents template.HTML
	types.RepoBlobResponse
}

func (p *Pages) RepoBlob(w io.Writer, params RepoBlobParams) error {
	style := styles.Get("bw")
	b := style.Builder()
	b.Add(chroma.LiteralString, "noitalic")
	style, _ = b.Build()

	if params.ShowRendered {
		switch markup.GetFormat(params.Path) {
		case markup.FormatMarkdown:
			params.RenderedContents = template.HTML(markup.RenderMarkdown(params.Contents))
		}
	}

	if params.Lines < 5000 {
		c := params.Contents
		formatter := chromahtml.New(
			chromahtml.InlineCode(false),
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
	LoggedInUser  *auth.User
	RepoInfo      RepoInfo
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
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Active       string
	Issues       []db.Issue
	DidHandleMap map[string]string

	FilteringByOpen bool
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

type EditIssueCommentParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Issue        *db.Issue
	Comment      *db.Comment
}

func (p *Pages) EditIssueCommentFragment(w io.Writer, params EditIssueCommentParams) error {
	return p.executePlain("repo/issues/fragments/editIssueComment", w, params)
}

type SingleIssueCommentParams struct {
	LoggedInUser *auth.User
	DidHandleMap map[string]string
	RepoInfo     RepoInfo
	Issue        *db.Issue
	Comment      *db.Comment
}

func (p *Pages) SingleIssueCommentFragment(w io.Writer, params SingleIssueCommentParams) error {
	return p.executePlain("repo/issues/fragments/issueComment", w, params)
}

type RepoNewPullParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Branches     []types.Branch
	Active       string
}

func (p *Pages) RepoNewPull(w io.Writer, params RepoNewPullParams) error {
	params.Active = "pulls"
	return p.executeRepo("repo/pulls/new", w, params)
}

type RepoPullsParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Pulls        []db.Pull
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
	LoggedInUser   *auth.User
	RepoInfo       RepoInfo
	Active         string
	DidHandleMap   map[string]string
	Pull           *db.Pull
	PullSourceRepo *db.Repo
	MergeCheck     types.MergeCheckResponse
	ResubmitCheck  ResubmitResult
}

func (p *Pages) RepoSinglePull(w io.Writer, params RepoSinglePullParams) error {
	params.Active = "pulls"
	return p.executeRepo("repo/pulls/pull", w, params)
}

type RepoPullPatchParams struct {
	LoggedInUser *auth.User
	DidHandleMap map[string]string
	RepoInfo     RepoInfo
	Pull         *db.Pull
	Diff         types.NiceDiff
	Round        int
	Submission   *db.PullSubmission
}

// this name is a mouthful
func (p *Pages) RepoPullPatchPage(w io.Writer, params RepoPullPatchParams) error {
	return p.execute("repo/pulls/patch", w, params)
}

type PullPatchUploadParams struct {
	RepoInfo RepoInfo
}

func (p *Pages) PullPatchUploadFragment(w io.Writer, params PullPatchUploadParams) error {
	return p.executePlain("repo/pulls/fragments/pullPatchUpload", w, params)
}

type PullCompareBranchesParams struct {
	RepoInfo RepoInfo
	Branches []types.Branch
}

func (p *Pages) PullCompareBranchesFragment(w io.Writer, params PullCompareBranchesParams) error {
	return p.executePlain("repo/pulls/fragments/pullCompareBranches", w, params)
}

type PullCompareForkParams struct {
	RepoInfo RepoInfo
	Forks    []db.Repo
}

func (p *Pages) PullCompareForkFragment(w io.Writer, params PullCompareForkParams) error {
	return p.executePlain("repo/pulls/fragments/pullCompareForks", w, params)
}

type PullCompareForkBranchesParams struct {
	RepoInfo       RepoInfo
	SourceBranches []types.Branch
	TargetBranches []types.Branch
}

func (p *Pages) PullCompareForkBranchesFragment(w io.Writer, params PullCompareForkBranchesParams) error {
	return p.executePlain("repo/pulls/fragments/pullCompareForksBranches", w, params)
}

type PullResubmitParams struct {
	LoggedInUser *auth.User
	RepoInfo     RepoInfo
	Pull         *db.Pull
	SubmissionId int
}

func (p *Pages) PullResubmitFragment(w io.Writer, params PullResubmitParams) error {
	return p.executePlain("repo/pulls/fragments/pullResubmit", w, params)
}

type PullActionsParams struct {
	LoggedInUser  *auth.User
	RepoInfo      RepoInfo
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
	RepoInfo     RepoInfo
	Pull         *db.Pull
	RoundNumber  int
}

func (p *Pages) PullNewCommentFragment(w io.Writer, params PullNewCommentParams) error {
	return p.executePlain("repo/pulls/fragments/pullNewComment", w, params)
}

func (p *Pages) Static() http.Handler {
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
