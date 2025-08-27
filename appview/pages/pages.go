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
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/commitverify"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/appview/pages/repoinfo"
	"tangled.sh/tangled.sh/core/appview/pagination"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/types"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

//go:embed templates/* static
var Files embed.FS

type Pages struct {
	mu    sync.RWMutex
	cache *TmplCache[string, *template.Template]

	avatar      config.AvatarConfig
	resolver    *idresolver.Resolver
	dev         bool
	embedFS     fs.FS
	templateDir string // Path to templates on disk for dev mode
	rctx        *markup.RenderContext
	logger      *slog.Logger
}

func NewPages(config *config.Config, res *idresolver.Resolver) *Pages {
	// initialized with safe defaults, can be overriden per use
	rctx := &markup.RenderContext{
		IsDev:      config.Core.Dev,
		CamoUrl:    config.Camo.Host,
		CamoSecret: config.Camo.SharedSecret,
		Sanitizer:  markup.NewSanitizer(),
	}

	p := &Pages{
		mu:          sync.RWMutex{},
		cache:       NewTmplCache[string, *template.Template](),
		dev:         config.Core.Dev,
		avatar:      config.Avatar,
		rctx:        rctx,
		resolver:    res,
		templateDir: "appview/pages",
		logger:      slog.Default().With("component", "pages"),
	}

	if p.dev {
		p.embedFS = os.DirFS(p.templateDir)
	} else {
		p.embedFS = Files
	}

	return p
}

func (p *Pages) pathToName(s string) string {
	return strings.TrimSuffix(strings.TrimPrefix(s, "templates/"), ".html")
}

// reverse of pathToName
func (p *Pages) nameToPath(s string) string {
	return "templates/" + s + ".html"
}

func (p *Pages) fragmentPaths() ([]string, error) {
	var fragmentPaths []string
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
		fragmentPaths = append(fragmentPaths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return fragmentPaths, nil
}

func (p *Pages) fragments() (*template.Template, error) {
	fragmentPaths, err := p.fragmentPaths()
	if err != nil {
		return nil, err
	}

	funcs := p.funcMap()

	// parse all fragments together
	allFragments := template.New("").Funcs(funcs)
	for _, f := range fragmentPaths {
		name := p.pathToName(f)

		pf, err := template.New(name).
			Funcs(funcs).
			ParseFS(p.embedFS, f)
		if err != nil {
			return nil, err
		}

		allFragments, err = allFragments.AddParseTree(name, pf.Tree)
		if err != nil {
			return nil, err
		}
	}

	return allFragments, nil
}

// parse without memoization
func (p *Pages) rawParse(stack ...string) (*template.Template, error) {
	paths, err := p.fragmentPaths()
	if err != nil {
		return nil, err
	}
	for _, s := range stack {
		paths = append(paths, p.nameToPath(s))
	}

	funcs := p.funcMap()
	top := stack[len(stack)-1]
	parsed, err := template.New(top).
		Funcs(funcs).
		ParseFS(p.embedFS, paths...)
	if err != nil {
		return nil, err
	}

	return parsed, nil
}

func (p *Pages) parse(stack ...string) (*template.Template, error) {
	key := strings.Join(stack, "|")

	// never cache in dev mode
	if cached, exists := p.cache.Get(key); !p.dev && exists {
		return cached, nil
	}

	result, err := p.rawParse(stack...)
	if err != nil {
		return nil, err
	}

	p.cache.Set(key, result)
	return result, nil
}

func (p *Pages) parseBase(top string) (*template.Template, error) {
	stack := []string{
		"layouts/base",
		top,
	}
	return p.parse(stack...)
}

func (p *Pages) parseRepoBase(top string) (*template.Template, error) {
	stack := []string{
		"layouts/base",
		"layouts/repobase",
		top,
	}
	return p.parse(stack...)
}

func (p *Pages) executePlain(name string, w io.Writer, params any) error {
	tpl, err := p.parse(name)
	if err != nil {
		return err
	}

	return tpl.Execute(w, params)
}

func (p *Pages) execute(name string, w io.Writer, params any) error {
	tpl, err := p.parseBase(name)
	if err != nil {
		return err
	}

	return tpl.ExecuteTemplate(w, "layouts/base", params)
}

func (p *Pages) executeRepo(name string, w io.Writer, params any) error {
	tpl, err := p.parseRepoBase(name)
	if err != nil {
		return err
	}

	return tpl.ExecuteTemplate(w, "layouts/base", params)
}

func (p *Pages) Favicon(w io.Writer) error {
	return p.executePlain("favicon", w, nil)
}

type LoginParams struct {
	ReturnUrl string
}

func (p *Pages) Login(w io.Writer, params LoginParams) error {
	return p.executePlain("user/login", w, params)
}

func (p *Pages) Signup(w io.Writer) error {
	return p.executePlain("user/signup", w, nil)
}

func (p *Pages) CompleteSignup(w io.Writer) error {
	return p.executePlain("user/completeSignup", w, nil)
}

type TermsOfServiceParams struct {
	LoggedInUser *oauth.User
}

func (p *Pages) TermsOfService(w io.Writer, params TermsOfServiceParams) error {
	return p.execute("legal/terms", w, params)
}

type PrivacyPolicyParams struct {
	LoggedInUser *oauth.User
}

func (p *Pages) PrivacyPolicy(w io.Writer, params PrivacyPolicyParams) error {
	return p.execute("legal/privacy", w, params)
}

type TimelineParams struct {
	LoggedInUser *oauth.User
	Timeline     []db.TimelineEvent
	Repos        []db.Repo
}

func (p *Pages) Timeline(w io.Writer, params TimelineParams) error {
	return p.execute("timeline/timeline", w, params)
}

type UserProfileSettingsParams struct {
	LoggedInUser *oauth.User
	Tabs         []map[string]any
	Tab          string
}

func (p *Pages) UserProfileSettings(w io.Writer, params UserProfileSettingsParams) error {
	return p.execute("user/settings/profile", w, params)
}

type UserKeysSettingsParams struct {
	LoggedInUser *oauth.User
	PubKeys      []db.PublicKey
	Tabs         []map[string]any
	Tab          string
}

func (p *Pages) UserKeysSettings(w io.Writer, params UserKeysSettingsParams) error {
	return p.execute("user/settings/keys", w, params)
}

type UserEmailsSettingsParams struct {
	LoggedInUser *oauth.User
	Emails       []db.Email
	Tabs         []map[string]any
	Tab          string
}

func (p *Pages) UserEmailsSettings(w io.Writer, params UserEmailsSettingsParams) error {
	return p.execute("user/settings/emails", w, params)
}

type KnotBannerParams struct {
	Registrations []db.Registration
}

func (p *Pages) KnotBanner(w io.Writer, params KnotBannerParams) error {
	return p.executePlain("knots/fragments/banner", w, params)
}

type KnotsParams struct {
	LoggedInUser  *oauth.User
	Registrations []db.Registration
}

func (p *Pages) Knots(w io.Writer, params KnotsParams) error {
	return p.execute("knots/index", w, params)
}

type KnotParams struct {
	LoggedInUser *oauth.User
	Registration *db.Registration
	Members      []string
	Repos        map[string][]db.Repo
	IsOwner      bool
}

func (p *Pages) Knot(w io.Writer, params KnotParams) error {
	return p.execute("knots/dashboard", w, params)
}

type KnotListingParams struct {
	*db.Registration
}

func (p *Pages) KnotListing(w io.Writer, params KnotListingParams) error {
	return p.executePlain("knots/fragments/knotListing", w, params)
}

type SpindlesParams struct {
	LoggedInUser *oauth.User
	Spindles     []db.Spindle
}

func (p *Pages) Spindles(w io.Writer, params SpindlesParams) error {
	return p.execute("spindles/index", w, params)
}

type SpindleListingParams struct {
	db.Spindle
}

func (p *Pages) SpindleListing(w io.Writer, params SpindleListingParams) error {
	return p.executePlain("spindles/fragments/spindleListing", w, params)
}

type SpindleDashboardParams struct {
	LoggedInUser *oauth.User
	Spindle      db.Spindle
	Members      []string
	Repos        map[string][]db.Repo
}

func (p *Pages) SpindleDashboard(w io.Writer, params SpindleDashboardParams) error {
	return p.execute("spindles/dashboard", w, params)
}

type NewRepoParams struct {
	LoggedInUser *oauth.User
	Knots        []string
}

func (p *Pages) NewRepo(w io.Writer, params NewRepoParams) error {
	return p.execute("repo/new", w, params)
}

type ForkRepoParams struct {
	LoggedInUser *oauth.User
	Knots        []string
	RepoInfo     repoinfo.RepoInfo
}

func (p *Pages) ForkRepo(w io.Writer, params ForkRepoParams) error {
	return p.execute("repo/fork", w, params)
}

type ProfileHomePageParams struct {
	LoggedInUser       *oauth.User
	Repos              []db.Repo
	CollaboratingRepos []db.Repo
	ProfileTimeline    *db.ProfileTimeline
	Card               ProfileCard
	Punchcard          db.Punchcard
}

type ProfileCard struct {
	UserDid        string
	UserHandle     string
	FollowStatus   db.FollowStatus
	FollowersCount int
	FollowingCount int

	Profile *db.Profile
}

func (p *Pages) ProfileHomePage(w io.Writer, params ProfileHomePageParams) error {
	return p.execute("user/profile", w, params)
}

type ReposPageParams struct {
	LoggedInUser *oauth.User
	Repos        []db.Repo
	Card         ProfileCard
}

func (p *Pages) ReposPage(w io.Writer, params ReposPageParams) error {
	return p.execute("user/repos", w, params)
}

type FollowCard struct {
	UserDid        string
	FollowStatus   db.FollowStatus
	FollowersCount int
	FollowingCount int
	Profile        *db.Profile
}

type FollowersPageParams struct {
	LoggedInUser *oauth.User
	Followers    []FollowCard
	Card         ProfileCard
}

func (p *Pages) FollowersPage(w io.Writer, params FollowersPageParams) error {
	return p.execute("user/followers", w, params)
}

type FollowingPageParams struct {
	LoggedInUser *oauth.User
	Following    []FollowCard
	Card         ProfileCard
}

func (p *Pages) FollowingPage(w io.Writer, params FollowingPageParams) error {
	return p.execute("user/following", w, params)
}

type FollowFragmentParams struct {
	UserDid      string
	FollowStatus db.FollowStatus
}

func (p *Pages) FollowFragment(w io.Writer, params FollowFragmentParams) error {
	return p.executePlain("user/fragments/follow", w, params)
}

type EditBioParams struct {
	LoggedInUser *oauth.User
	Profile      *db.Profile
}

func (p *Pages) EditBioFragment(w io.Writer, params EditBioParams) error {
	return p.executePlain("user/fragments/editBio", w, params)
}

type EditPinsParams struct {
	LoggedInUser *oauth.User
	Profile      *db.Profile
	AllRepos     []PinnedRepo
}

type PinnedRepo struct {
	IsPinned bool
	db.Repo
}

func (p *Pages) EditPinsFragment(w io.Writer, params EditPinsParams) error {
	return p.executePlain("user/fragments/editPins", w, params)
}

type RepoStarFragmentParams struct {
	IsStarred bool
	RepoAt    syntax.ATURI
	Stats     db.RepoStats
}

func (p *Pages) RepoStarFragment(w io.Writer, params RepoStarFragmentParams) error {
	return p.executePlain("repo/fragments/repoStar", w, params)
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
	LoggedInUser  *oauth.User
	RepoInfo      repoinfo.RepoInfo
	Active        string
	TagMap        map[string][]string
	CommitsTrunc  []*object.Commit
	TagsTrunc     []*types.TagReference
	BranchesTrunc []types.Branch
	// ForkInfo           *types.ForkInfo
	HTMLReadme         template.HTML
	Raw                bool
	EmailToDidOrHandle map[string]string
	VerifiedCommits    commitverify.VerifiedCommits
	Languages          []types.RepoLanguageDetails
	Pipelines          map[string]db.Pipeline
	types.RepoIndexResponse
}

func (p *Pages) RepoIndexPage(w io.Writer, params RepoIndexParams) error {
	params.Active = "overview"
	if params.IsEmpty {
		return p.executeRepo("repo/empty", w, params)
	}

	p.rctx.RepoInfo = params.RepoInfo
	p.rctx.RepoInfo.Ref = params.Ref
	p.rctx.RendererType = markup.RendererTypeRepoMarkdown

	if params.ReadmeFileName != "" {
		ext := filepath.Ext(params.ReadmeFileName)
		switch ext {
		case ".md", ".markdown", ".mdown", ".mkdn", ".mkd":
			params.Raw = false
			htmlString := p.rctx.RenderMarkdown(params.Readme)
			sanitized := p.rctx.SanitizeDefault(htmlString)
			params.HTMLReadme = template.HTML(sanitized)
		default:
			params.Raw = true
		}
	}

	return p.executeRepo("repo/index", w, params)
}

type RepoLogParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	TagMap       map[string][]string
	types.RepoLogResponse
	Active             string
	EmailToDidOrHandle map[string]string
	VerifiedCommits    commitverify.VerifiedCommits
	Pipelines          map[string]db.Pipeline
}

func (p *Pages) RepoLog(w io.Writer, params RepoLogParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/log", w, params)
}

type RepoCommitParams struct {
	LoggedInUser       *oauth.User
	RepoInfo           repoinfo.RepoInfo
	Active             string
	EmailToDidOrHandle map[string]string
	Pipeline           *db.Pipeline
	DiffOpts           types.DiffOpts

	// singular because it's always going to be just one
	VerifiedCommit commitverify.VerifiedCommits

	types.RepoCommitResponse
}

func (p *Pages) RepoCommit(w io.Writer, params RepoCommitParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/commit", w, params)
}

type RepoTreeParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Active       string
	BreadCrumbs  [][]string
	TreePath     string
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
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Active       string
	types.RepoBranchesResponse
}

func (p *Pages) RepoBranches(w io.Writer, params RepoBranchesParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/branches", w, params)
}

type RepoTagsParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Active       string
	types.RepoTagsResponse
	ArtifactMap       map[plumbing.Hash][]db.Artifact
	DanglingArtifacts []db.Artifact
}

func (p *Pages) RepoTags(w io.Writer, params RepoTagsParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/tags", w, params)
}

type RepoArtifactParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Artifact     db.Artifact
}

func (p *Pages) RepoArtifactFragment(w io.Writer, params RepoArtifactParams) error {
	return p.executePlain("repo/fragments/artifact", w, params)
}

type RepoBlobParams struct {
	LoggedInUser     *oauth.User
	RepoInfo         repoinfo.RepoInfo
	Active           string
	Unsupported      bool
	IsImage          bool
	IsVideo          bool
	ContentSrc       string
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
			p.rctx.RepoInfo = params.RepoInfo
			p.rctx.RendererType = markup.RendererTypeRepoMarkdown
			htmlString := p.rctx.RenderMarkdown(params.Contents)
			sanitized := p.rctx.SanitizeDefault(htmlString)
			params.RenderedContents = template.HTML(sanitized)
		}
	}

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
	params.Active = "overview"
	return p.executeRepo("repo/blob", w, params)
}

type Collaborator struct {
	Did    string
	Handle string
	Role   string
}

type RepoSettingsParams struct {
	LoggedInUser   *oauth.User
	RepoInfo       repoinfo.RepoInfo
	Collaborators  []Collaborator
	Active         string
	Branches       []types.Branch
	Spindles       []string
	CurrentSpindle string
	Secrets        []*tangled.RepoListSecrets_Secret

	// TODO: use repoinfo.roles
	IsCollaboratorInviteAllowed bool
}

func (p *Pages) RepoSettings(w io.Writer, params RepoSettingsParams) error {
	params.Active = "settings"
	return p.executeRepo("repo/settings", w, params)
}

type RepoGeneralSettingsParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Active       string
	Tabs         []map[string]any
	Tab          string
	Branches     []types.Branch
}

func (p *Pages) RepoGeneralSettings(w io.Writer, params RepoGeneralSettingsParams) error {
	params.Active = "settings"
	return p.executeRepo("repo/settings/general", w, params)
}

type RepoAccessSettingsParams struct {
	LoggedInUser  *oauth.User
	RepoInfo      repoinfo.RepoInfo
	Active        string
	Tabs          []map[string]any
	Tab           string
	Collaborators []Collaborator
}

func (p *Pages) RepoAccessSettings(w io.Writer, params RepoAccessSettingsParams) error {
	params.Active = "settings"
	return p.executeRepo("repo/settings/access", w, params)
}

type RepoPipelineSettingsParams struct {
	LoggedInUser   *oauth.User
	RepoInfo       repoinfo.RepoInfo
	Active         string
	Tabs           []map[string]any
	Tab            string
	Spindles       []string
	CurrentSpindle string
	Secrets        []map[string]any
}

func (p *Pages) RepoPipelineSettings(w io.Writer, params RepoPipelineSettingsParams) error {
	params.Active = "settings"
	return p.executeRepo("repo/settings/pipelines", w, params)
}

type RepoIssuesParams struct {
	LoggedInUser    *oauth.User
	RepoInfo        repoinfo.RepoInfo
	Active          string
	Issues          []db.Issue
	Page            pagination.Page
	FilteringByOpen bool
}

func (p *Pages) RepoIssues(w io.Writer, params RepoIssuesParams) error {
	params.Active = "issues"
	return p.executeRepo("repo/issues/issues", w, params)
}

type RepoSingleIssueParams struct {
	LoggedInUser     *oauth.User
	RepoInfo         repoinfo.RepoInfo
	Active           string
	Issue            *db.Issue
	Comments         []db.Comment
	IssueOwnerHandle string

	OrderedReactionKinds []db.ReactionKind
	Reactions            map[db.ReactionKind]int
	UserReacted          map[db.ReactionKind]bool

	State string
}

type ThreadReactionFragmentParams struct {
	ThreadAt  syntax.ATURI
	Kind      db.ReactionKind
	Count     int
	IsReacted bool
}

func (p *Pages) ThreadReactionFragment(w io.Writer, params ThreadReactionFragmentParams) error {
	return p.executePlain("repo/fragments/reaction", w, params)
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
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Active       string
}

func (p *Pages) RepoNewIssue(w io.Writer, params RepoNewIssueParams) error {
	params.Active = "issues"
	return p.executeRepo("repo/issues/new", w, params)
}

type EditIssueCommentParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *db.Issue
	Comment      *db.Comment
}

func (p *Pages) EditIssueCommentFragment(w io.Writer, params EditIssueCommentParams) error {
	return p.executePlain("repo/issues/fragments/editIssueComment", w, params)
}

type SingleIssueCommentParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *db.Issue
	Comment      *db.Comment
}

func (p *Pages) SingleIssueCommentFragment(w io.Writer, params SingleIssueCommentParams) error {
	return p.executePlain("repo/issues/fragments/issueComment", w, params)
}

type RepoNewPullParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Branches     []types.Branch
	Strategy     string
	SourceBranch string
	TargetBranch string
	Title        string
	Body         string
	Active       string
}

func (p *Pages) RepoNewPull(w io.Writer, params RepoNewPullParams) error {
	params.Active = "pulls"
	return p.executeRepo("repo/pulls/new", w, params)
}

type RepoPullsParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Pulls        []*db.Pull
	Active       string
	FilteringBy  db.PullState
	Stacks       map[string]db.Stack
	Pipelines    map[string]db.Pipeline
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
	LoggedInUser   *oauth.User
	RepoInfo       repoinfo.RepoInfo
	Active         string
	Pull           *db.Pull
	Stack          db.Stack
	AbandonedPulls []*db.Pull
	MergeCheck     types.MergeCheckResponse
	ResubmitCheck  ResubmitResult
	Pipelines      map[string]db.Pipeline

	OrderedReactionKinds []db.ReactionKind
	Reactions            map[db.ReactionKind]int
	UserReacted          map[db.ReactionKind]bool
}

func (p *Pages) RepoSinglePull(w io.Writer, params RepoSinglePullParams) error {
	params.Active = "pulls"
	return p.executeRepo("repo/pulls/pull", w, params)
}

type RepoPullPatchParams struct {
	LoggedInUser         *oauth.User
	RepoInfo             repoinfo.RepoInfo
	Pull                 *db.Pull
	Stack                db.Stack
	Diff                 *types.NiceDiff
	Round                int
	Submission           *db.PullSubmission
	OrderedReactionKinds []db.ReactionKind
	DiffOpts             types.DiffOpts
}

// this name is a mouthful
func (p *Pages) RepoPullPatchPage(w io.Writer, params RepoPullPatchParams) error {
	return p.execute("repo/pulls/patch", w, params)
}

type RepoPullInterdiffParams struct {
	LoggedInUser         *oauth.User
	RepoInfo             repoinfo.RepoInfo
	Pull                 *db.Pull
	Round                int
	Interdiff            *patchutil.InterdiffResult
	OrderedReactionKinds []db.ReactionKind
	DiffOpts             types.DiffOpts
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
	RepoInfo     repoinfo.RepoInfo
	Branches     []types.Branch
	SourceBranch string
}

func (p *Pages) PullCompareBranchesFragment(w io.Writer, params PullCompareBranchesParams) error {
	return p.executePlain("repo/pulls/fragments/pullCompareBranches", w, params)
}

type PullCompareForkParams struct {
	RepoInfo repoinfo.RepoInfo
	Forks    []db.Repo
	Selected string
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
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Pull         *db.Pull
	SubmissionId int
}

func (p *Pages) PullResubmitFragment(w io.Writer, params PullResubmitParams) error {
	return p.executePlain("repo/pulls/fragments/pullResubmit", w, params)
}

type PullActionsParams struct {
	LoggedInUser  *oauth.User
	RepoInfo      repoinfo.RepoInfo
	Pull          *db.Pull
	RoundNumber   int
	MergeCheck    types.MergeCheckResponse
	ResubmitCheck ResubmitResult
	Stack         db.Stack
}

func (p *Pages) PullActionsFragment(w io.Writer, params PullActionsParams) error {
	return p.executePlain("repo/pulls/fragments/pullActions", w, params)
}

type PullNewCommentParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Pull         *db.Pull
	RoundNumber  int
}

func (p *Pages) PullNewCommentFragment(w io.Writer, params PullNewCommentParams) error {
	return p.executePlain("repo/pulls/fragments/pullNewComment", w, params)
}

type RepoCompareParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Forks        []db.Repo
	Branches     []types.Branch
	Tags         []*types.TagReference
	Base         string
	Head         string
	Diff         *types.NiceDiff
	DiffOpts     types.DiffOpts

	Active string
}

func (p *Pages) RepoCompare(w io.Writer, params RepoCompareParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/compare/compare", w, params)
}

type RepoCompareNewParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Forks        []db.Repo
	Branches     []types.Branch
	Tags         []*types.TagReference
	Base         string
	Head         string

	Active string
}

func (p *Pages) RepoCompareNew(w io.Writer, params RepoCompareNewParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/compare/new", w, params)
}

type RepoCompareAllowPullParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Base         string
	Head         string
}

func (p *Pages) RepoCompareAllowPullFragment(w io.Writer, params RepoCompareAllowPullParams) error {
	return p.executePlain("repo/fragments/compareAllowPull", w, params)
}

type RepoCompareDiffParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Diff         types.NiceDiff
}

func (p *Pages) RepoCompareDiff(w io.Writer, params RepoCompareDiffParams) error {
	return p.executePlain("repo/fragments/diff", w, []any{params.RepoInfo.FullName, &params.Diff})
}

type PipelinesParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Pipelines    []db.Pipeline
	Active       string
}

func (p *Pages) Pipelines(w io.Writer, params PipelinesParams) error {
	params.Active = "pipelines"
	return p.executeRepo("repo/pipelines/pipelines", w, params)
}

type LogBlockParams struct {
	Id        int
	Name      string
	Command   string
	Collapsed bool
}

func (p *Pages) LogBlock(w io.Writer, params LogBlockParams) error {
	return p.executePlain("repo/pipelines/fragments/logBlock", w, params)
}

type LogLineParams struct {
	Id      int
	Content string
}

func (p *Pages) LogLine(w io.Writer, params LogLineParams) error {
	return p.executePlain("repo/pipelines/fragments/logLine", w, params)
}

type WorkflowParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Pipeline     db.Pipeline
	Workflow     string
	LogUrl       string
	Active       string
}

func (p *Pages) Workflow(w io.Writer, params WorkflowParams) error {
	params.Active = "pipelines"
	return p.executeRepo("repo/pipelines/workflow", w, params)
}

type PutStringParams struct {
	LoggedInUser *oauth.User
	Action       string

	// this is supplied in the case of editing an existing string
	String db.String
}

func (p *Pages) PutString(w io.Writer, params PutStringParams) error {
	return p.execute("strings/put", w, params)
}

type StringsDashboardParams struct {
	LoggedInUser *oauth.User
	Card         ProfileCard
	Strings      []db.String
}

func (p *Pages) StringsDashboard(w io.Writer, params StringsDashboardParams) error {
	return p.execute("strings/dashboard", w, params)
}

type StringTimelineParams struct {
	LoggedInUser *oauth.User
	Strings      []db.String
}

func (p *Pages) StringsTimeline(w io.Writer, params StringTimelineParams) error {
	return p.execute("strings/timeline", w, params)
}

type SingleStringParams struct {
	LoggedInUser     *oauth.User
	ShowRendered     bool
	RenderToggle     bool
	RenderedContents template.HTML
	String           db.String
	Stats            db.StringStats
	Owner            identity.Identity
}

func (p *Pages) SingleString(w io.Writer, params SingleStringParams) error {
	var style *chroma.Style = styles.Get("catpuccin-latte")

	if params.ShowRendered {
		switch markup.GetFormat(params.String.Filename) {
		case markup.FormatMarkdown:
			p.rctx.RendererType = markup.RendererTypeRepoMarkdown
			htmlString := p.rctx.RenderMarkdown(params.String.Contents)
			sanitized := p.rctx.SanitizeDefault(htmlString)
			params.RenderedContents = template.HTML(sanitized)
		}
	}

	c := params.String.Contents
	formatter := chromahtml.New(
		chromahtml.InlineCode(false),
		chromahtml.WithLineNumbers(true),
		chromahtml.WithLinkableLineNumbers(true, "L"),
		chromahtml.Standalone(false),
		chromahtml.WithClasses(true),
	)

	lexer := lexers.Get(filepath.Base(params.String.Filename))
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

	params.String.Contents = code.String()
	return p.execute("strings/string", w, params)
}

func (p *Pages) Static() http.Handler {
	if p.dev {
		return http.StripPrefix("/static/", http.FileServer(http.Dir("appview/pages/static")))
	}

	sub, err := fs.Sub(Files, "static")
	if err != nil {
		p.logger.Error("no static dir found? that's crazy", "err", err)
		panic(err)
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
		slog.Debug("Error opening CSS file", "err", err)
		return ""
	}
	defer cssFile.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, cssFile); err != nil {
		slog.Debug("Error hashing CSS file", "err", err)
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

func (p *Pages) ErrorKnot404(w io.Writer) error {
	return p.execute("errors/knot404", w, nil)
}

func (p *Pages) Error503(w io.Writer) error {
	return p.execute("errors/503", w, nil)
}
