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

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/commitverify"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/appview/pages/repoinfo"
	"tangled.org/core/appview/pagination"
	"tangled.org/core/idresolver"
	"tangled.org/core/patchutil"
	"tangled.org/core/types"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

//go:embed templates/* static legal
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

func (p *Pages) parseProfileBase(top string) (*template.Template, error) {
	stack := []string{
		"layouts/base",
		"layouts/profilebase",
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

func (p *Pages) executeProfile(name string, w io.Writer, params any) error {
	tpl, err := p.parseProfileBase(name)
	if err != nil {
		return err
	}

	return tpl.ExecuteTemplate(w, "layouts/base", params)
}

func (p *Pages) Favicon(w io.Writer) error {
	return p.executePlain("fragments/dolly/silhouette", w, nil)
}

type LoginParams struct {
	ReturnUrl string
}

func (p *Pages) Login(w io.Writer, params LoginParams) error {
	return p.executePlain("user/login", w, params)
}

type SignupParams struct {
	CloudflareSiteKey string
}

func (p *Pages) Signup(w io.Writer, params SignupParams) error {
	return p.executePlain("user/signup", w, params)
}

func (p *Pages) CompleteSignup(w io.Writer) error {
	return p.executePlain("user/completeSignup", w, nil)
}

type TermsOfServiceParams struct {
	LoggedInUser *oauth.User
	Content      template.HTML
}

func (p *Pages) TermsOfService(w io.Writer, params TermsOfServiceParams) error {
	filename := "terms.md"
	filePath := filepath.Join("legal", filename)

	file, err := p.embedFS.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filename, err)
	}
	defer file.Close()

	markdownBytes, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filename, err)
	}

	p.rctx.RendererType = markup.RendererTypeDefault
	htmlString := p.rctx.RenderMarkdown(string(markdownBytes))
	sanitized := p.rctx.SanitizeDefault(htmlString)
	params.Content = template.HTML(sanitized)

	return p.execute("legal/terms", w, params)
}

type PrivacyPolicyParams struct {
	LoggedInUser *oauth.User
	Content      template.HTML
}

func (p *Pages) PrivacyPolicy(w io.Writer, params PrivacyPolicyParams) error {
	filename := "privacy.md"
	filePath := filepath.Join("legal", filename)

	file, err := p.embedFS.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filename, err)
	}
	defer file.Close()

	markdownBytes, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filename, err)
	}

	p.rctx.RendererType = markup.RendererTypeDefault
	htmlString := p.rctx.RenderMarkdown(string(markdownBytes))
	sanitized := p.rctx.SanitizeDefault(htmlString)
	params.Content = template.HTML(sanitized)

	return p.execute("legal/privacy", w, params)
}

type BrandParams struct {
	LoggedInUser *oauth.User
}

func (p *Pages) Brand(w io.Writer, params BrandParams) error {
	return p.execute("brand/brand", w, params)
}

type TimelineParams struct {
	LoggedInUser *oauth.User
	Timeline     []models.TimelineEvent
	Repos        []models.Repo
	GfiLabel     *models.LabelDefinition
}

func (p *Pages) Timeline(w io.Writer, params TimelineParams) error {
	return p.execute("timeline/timeline", w, params)
}

type GoodFirstIssuesParams struct {
	LoggedInUser *oauth.User
	Issues       []models.Issue
	RepoGroups   []*models.RepoGroup
	LabelDefs    map[string]*models.LabelDefinition
	GfiLabel     *models.LabelDefinition
	Page         pagination.Page
}

func (p *Pages) GoodFirstIssues(w io.Writer, params GoodFirstIssuesParams) error {
	return p.execute("goodfirstissues/index", w, params)
}

type UserProfileSettingsParams struct {
	LoggedInUser *oauth.User
	Tabs         []map[string]any
	Tab          string
}

func (p *Pages) UserProfileSettings(w io.Writer, params UserProfileSettingsParams) error {
	return p.execute("user/settings/profile", w, params)
}

type NotificationsParams struct {
	LoggedInUser  *oauth.User
	Notifications []*models.NotificationWithEntity
	UnreadCount   int
	Page          pagination.Page
	Total         int64
}

func (p *Pages) Notifications(w io.Writer, params NotificationsParams) error {
	return p.execute("notifications/list", w, params)
}

type NotificationItemParams struct {
	Notification *models.Notification
}

func (p *Pages) NotificationItem(w io.Writer, params NotificationItemParams) error {
	return p.executePlain("notifications/fragments/item", w, params)
}

type NotificationCountParams struct {
	Count int64
}

func (p *Pages) NotificationCount(w io.Writer, params NotificationCountParams) error {
	return p.executePlain("notifications/fragments/count", w, params)
}

type UserKeysSettingsParams struct {
	LoggedInUser *oauth.User
	PubKeys      []models.PublicKey
	Tabs         []map[string]any
	Tab          string
}

func (p *Pages) UserKeysSettings(w io.Writer, params UserKeysSettingsParams) error {
	return p.execute("user/settings/keys", w, params)
}

type UserEmailsSettingsParams struct {
	LoggedInUser *oauth.User
	Emails       []models.Email
	Tabs         []map[string]any
	Tab          string
}

func (p *Pages) UserEmailsSettings(w io.Writer, params UserEmailsSettingsParams) error {
	return p.execute("user/settings/emails", w, params)
}

type UserNotificationSettingsParams struct {
	LoggedInUser *oauth.User
	Preferences  *models.NotificationPreferences
	Tabs         []map[string]any
	Tab          string
}

func (p *Pages) UserNotificationSettings(w io.Writer, params UserNotificationSettingsParams) error {
	return p.execute("user/settings/notifications", w, params)
}

type UpgradeBannerParams struct {
	Registrations []models.Registration
	Spindles      []models.Spindle
}

func (p *Pages) UpgradeBanner(w io.Writer, params UpgradeBannerParams) error {
	return p.executePlain("banner", w, params)
}

type KnotsParams struct {
	LoggedInUser  *oauth.User
	Registrations []models.Registration
}

func (p *Pages) Knots(w io.Writer, params KnotsParams) error {
	return p.execute("knots/index", w, params)
}

type KnotParams struct {
	LoggedInUser *oauth.User
	Registration *models.Registration
	Members      []string
	Repos        map[string][]models.Repo
	IsOwner      bool
}

func (p *Pages) Knot(w io.Writer, params KnotParams) error {
	return p.execute("knots/dashboard", w, params)
}

type KnotListingParams struct {
	*models.Registration
}

func (p *Pages) KnotListing(w io.Writer, params KnotListingParams) error {
	return p.executePlain("knots/fragments/knotListing", w, params)
}

type SpindlesParams struct {
	LoggedInUser *oauth.User
	Spindles     []models.Spindle
}

func (p *Pages) Spindles(w io.Writer, params SpindlesParams) error {
	return p.execute("spindles/index", w, params)
}

type SpindleListingParams struct {
	models.Spindle
}

func (p *Pages) SpindleListing(w io.Writer, params SpindleListingParams) error {
	return p.executePlain("spindles/fragments/spindleListing", w, params)
}

type SpindleDashboardParams struct {
	LoggedInUser *oauth.User
	Spindle      models.Spindle
	Members      []string
	Repos        map[string][]models.Repo
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

type ProfileCard struct {
	UserDid      string
	UserHandle   string
	FollowStatus models.FollowStatus
	Punchcard    *models.Punchcard
	Profile      *models.Profile
	Stats        ProfileStats
	Active       string
}

type ProfileStats struct {
	RepoCount      int64
	StarredCount   int64
	StringCount    int64
	FollowersCount int64
	FollowingCount int64
}

func (p *ProfileCard) GetTabs() [][]any {
	tabs := [][]any{
		{"overview", "overview", "square-chart-gantt", nil},
		{"repos", "repos", "book-marked", p.Stats.RepoCount},
		{"starred", "starred", "star", p.Stats.StarredCount},
		{"strings", "strings", "line-squiggle", p.Stats.StringCount},
	}

	return tabs
}

type ProfileOverviewParams struct {
	LoggedInUser       *oauth.User
	Repos              []models.Repo
	CollaboratingRepos []models.Repo
	ProfileTimeline    *models.ProfileTimeline
	Card               *ProfileCard
	Active             string
}

func (p *Pages) ProfileOverview(w io.Writer, params ProfileOverviewParams) error {
	params.Active = "overview"
	return p.executeProfile("user/overview", w, params)
}

type ProfileReposParams struct {
	LoggedInUser *oauth.User
	Repos        []models.Repo
	Card         *ProfileCard
	Active       string
}

func (p *Pages) ProfileRepos(w io.Writer, params ProfileReposParams) error {
	params.Active = "repos"
	return p.executeProfile("user/repos", w, params)
}

type ProfileStarredParams struct {
	LoggedInUser *oauth.User
	Repos        []models.Repo
	Card         *ProfileCard
	Active       string
}

func (p *Pages) ProfileStarred(w io.Writer, params ProfileStarredParams) error {
	params.Active = "starred"
	return p.executeProfile("user/starred", w, params)
}

type ProfileStringsParams struct {
	LoggedInUser *oauth.User
	Strings      []models.String
	Card         *ProfileCard
	Active       string
}

func (p *Pages) ProfileStrings(w io.Writer, params ProfileStringsParams) error {
	params.Active = "strings"
	return p.executeProfile("user/strings", w, params)
}

type FollowCard struct {
	UserDid        string
	LoggedInUser   *oauth.User
	FollowStatus   models.FollowStatus
	FollowersCount int64
	FollowingCount int64
	Profile        *models.Profile
}

type ProfileFollowersParams struct {
	LoggedInUser *oauth.User
	Followers    []FollowCard
	Card         *ProfileCard
	Active       string
}

func (p *Pages) ProfileFollowers(w io.Writer, params ProfileFollowersParams) error {
	params.Active = "overview"
	return p.executeProfile("user/followers", w, params)
}

type ProfileFollowingParams struct {
	LoggedInUser *oauth.User
	Following    []FollowCard
	Card         *ProfileCard
	Active       string
}

func (p *Pages) ProfileFollowing(w io.Writer, params ProfileFollowingParams) error {
	params.Active = "overview"
	return p.executeProfile("user/following", w, params)
}

type FollowFragmentParams struct {
	UserDid      string
	FollowStatus models.FollowStatus
}

func (p *Pages) FollowFragment(w io.Writer, params FollowFragmentParams) error {
	return p.executePlain("user/fragments/follow", w, params)
}

type EditBioParams struct {
	LoggedInUser *oauth.User
	Profile      *models.Profile
}

func (p *Pages) EditBioFragment(w io.Writer, params EditBioParams) error {
	return p.executePlain("user/fragments/editBio", w, params)
}

type EditPinsParams struct {
	LoggedInUser *oauth.User
	Profile      *models.Profile
	AllRepos     []PinnedRepo
}

type PinnedRepo struct {
	IsPinned bool
	models.Repo
}

func (p *Pages) EditPinsFragment(w io.Writer, params EditPinsParams) error {
	return p.executePlain("user/fragments/editPins", w, params)
}

type RepoStarFragmentParams struct {
	IsStarred bool
	RepoAt    syntax.ATURI
	Stats     models.RepoStats
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
	Pipelines          map[string]models.Pipeline
	NeedsKnotUpgrade   bool
	types.RepoIndexResponse
}

func (p *Pages) RepoIndexPage(w io.Writer, params RepoIndexParams) error {
	params.Active = "overview"
	if params.IsEmpty {
		return p.executeRepo("repo/empty", w, params)
	}

	if params.NeedsKnotUpgrade {
		return p.executeRepo("repo/needsUpgrade", w, params)
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
	Pipelines          map[string]models.Pipeline
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
	Pipeline           *models.Pipeline
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
	Raw          bool
	HTMLReadme   template.HTML
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

	return p.executeRepo("repo/tree", w, params)
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
	ArtifactMap       map[plumbing.Hash][]models.Artifact
	DanglingArtifacts []models.Artifact
}

func (p *Pages) RepoTags(w io.Writer, params RepoTagsParams) error {
	params.Active = "overview"
	return p.executeRepo("repo/tags", w, params)
}

type RepoArtifactParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Artifact     models.Artifact
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
	*tangled.RepoBlob_Output
	// Computed fields for template compatibility
	Contents string
	Lines    int
	SizeHint uint64
	IsBinary bool
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
	LoggedInUser       *oauth.User
	RepoInfo           repoinfo.RepoInfo
	Labels             []models.LabelDefinition
	DefaultLabels      []models.LabelDefinition
	SubscribedLabels   map[string]struct{}
	ShouldSubscribeAll bool
	Active             string
	Tabs               []map[string]any
	Tab                string
	Branches           []types.Branch
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
	Issues          []models.Issue
	LabelDefs       map[string]*models.LabelDefinition
	Page            pagination.Page
	FilteringByOpen bool
}

func (p *Pages) RepoIssues(w io.Writer, params RepoIssuesParams) error {
	params.Active = "issues"
	return p.executeRepo("repo/issues/issues", w, params)
}

type RepoSingleIssueParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Active       string
	Issue        *models.Issue
	CommentList  []models.CommentListItem
	LabelDefs    map[string]*models.LabelDefinition

	OrderedReactionKinds []models.ReactionKind
	Reactions            map[models.ReactionKind]models.ReactionDisplayData
	UserReacted          map[models.ReactionKind]bool
}

func (p *Pages) RepoSingleIssue(w io.Writer, params RepoSingleIssueParams) error {
	params.Active = "issues"
	return p.executeRepo("repo/issues/issue", w, params)
}

type EditIssueParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *models.Issue
	Action       string
}

func (p *Pages) EditIssueFragment(w io.Writer, params EditIssueParams) error {
	params.Action = "edit"
	return p.executePlain("repo/issues/fragments/putIssue", w, params)
}

type ThreadReactionFragmentParams struct {
	ThreadAt  syntax.ATURI
	Kind      models.ReactionKind
	Count     int
	Users     []string
	IsReacted bool
}

func (p *Pages) ThreadReactionFragment(w io.Writer, params ThreadReactionFragmentParams) error {
	return p.executePlain("repo/fragments/reaction", w, params)
}

type RepoNewIssueParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *models.Issue // existing issue if any -- passed when editing
	Active       string
	Action       string
}

func (p *Pages) RepoNewIssue(w io.Writer, params RepoNewIssueParams) error {
	params.Active = "issues"
	params.Action = "create"
	return p.executeRepo("repo/issues/new", w, params)
}

type EditIssueCommentParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *models.Issue
	Comment      *models.IssueComment
}

func (p *Pages) EditIssueCommentFragment(w io.Writer, params EditIssueCommentParams) error {
	return p.executePlain("repo/issues/fragments/editIssueComment", w, params)
}

type ReplyIssueCommentPlaceholderParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *models.Issue
	Comment      *models.IssueComment
}

func (p *Pages) ReplyIssueCommentPlaceholderFragment(w io.Writer, params ReplyIssueCommentPlaceholderParams) error {
	return p.executePlain("repo/issues/fragments/replyIssueCommentPlaceholder", w, params)
}

type ReplyIssueCommentParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *models.Issue
	Comment      *models.IssueComment
}

func (p *Pages) ReplyIssueCommentFragment(w io.Writer, params ReplyIssueCommentParams) error {
	return p.executePlain("repo/issues/fragments/replyComment", w, params)
}

type IssueCommentBodyParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Issue        *models.Issue
	Comment      *models.IssueComment
}

func (p *Pages) IssueCommentBodyFragment(w io.Writer, params IssueCommentBodyParams) error {
	return p.executePlain("repo/issues/fragments/issueCommentBody", w, params)
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
	Pulls        []*models.Pull
	Active       string
	FilteringBy  models.PullState
	Stacks       map[string]models.Stack
	Pipelines    map[string]models.Pipeline
	LabelDefs    map[string]*models.LabelDefinition
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
	Pull           *models.Pull
	Stack          models.Stack
	AbandonedPulls []*models.Pull
	MergeCheck     types.MergeCheckResponse
	ResubmitCheck  ResubmitResult
	Pipelines      map[string]models.Pipeline

	OrderedReactionKinds []models.ReactionKind
	Reactions            map[models.ReactionKind]models.ReactionDisplayData
	UserReacted          map[models.ReactionKind]bool

	LabelDefs map[string]*models.LabelDefinition
}

func (p *Pages) RepoSinglePull(w io.Writer, params RepoSinglePullParams) error {
	params.Active = "pulls"
	return p.executeRepo("repo/pulls/pull", w, params)
}

type RepoPullPatchParams struct {
	LoggedInUser         *oauth.User
	RepoInfo             repoinfo.RepoInfo
	Pull                 *models.Pull
	Stack                models.Stack
	Diff                 *types.NiceDiff
	Round                int
	Submission           *models.PullSubmission
	OrderedReactionKinds []models.ReactionKind
	DiffOpts             types.DiffOpts
}

// this name is a mouthful
func (p *Pages) RepoPullPatchPage(w io.Writer, params RepoPullPatchParams) error {
	return p.execute("repo/pulls/patch", w, params)
}

type RepoPullInterdiffParams struct {
	LoggedInUser         *oauth.User
	RepoInfo             repoinfo.RepoInfo
	Pull                 *models.Pull
	Round                int
	Interdiff            *patchutil.InterdiffResult
	OrderedReactionKinds []models.ReactionKind
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
	Forks    []models.Repo
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
	Pull         *models.Pull
	SubmissionId int
}

func (p *Pages) PullResubmitFragment(w io.Writer, params PullResubmitParams) error {
	return p.executePlain("repo/pulls/fragments/pullResubmit", w, params)
}

type PullActionsParams struct {
	LoggedInUser  *oauth.User
	RepoInfo      repoinfo.RepoInfo
	Pull          *models.Pull
	RoundNumber   int
	MergeCheck    types.MergeCheckResponse
	ResubmitCheck ResubmitResult
	Stack         models.Stack
}

func (p *Pages) PullActionsFragment(w io.Writer, params PullActionsParams) error {
	return p.executePlain("repo/pulls/fragments/pullActions", w, params)
}

type PullNewCommentParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Pull         *models.Pull
	RoundNumber  int
}

func (p *Pages) PullNewCommentFragment(w io.Writer, params PullNewCommentParams) error {
	return p.executePlain("repo/pulls/fragments/pullNewComment", w, params)
}

type RepoCompareParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Forks        []models.Repo
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
	Forks        []models.Repo
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

type LabelPanelParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Defs         map[string]*models.LabelDefinition
	Subject      string
	State        models.LabelState
}

func (p *Pages) LabelPanel(w io.Writer, params LabelPanelParams) error {
	return p.executePlain("repo/fragments/labelPanel", w, params)
}

type EditLabelPanelParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Defs         map[string]*models.LabelDefinition
	Subject      string
	State        models.LabelState
}

func (p *Pages) EditLabelPanel(w io.Writer, params EditLabelPanelParams) error {
	return p.executePlain("repo/fragments/editLabelPanel", w, params)
}

type PipelinesParams struct {
	LoggedInUser *oauth.User
	RepoInfo     repoinfo.RepoInfo
	Pipelines    []models.Pipeline
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
	Pipeline     models.Pipeline
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
	String models.String
}

func (p *Pages) PutString(w io.Writer, params PutStringParams) error {
	return p.execute("strings/put", w, params)
}

type StringsDashboardParams struct {
	LoggedInUser *oauth.User
	Card         ProfileCard
	Strings      []models.String
}

func (p *Pages) StringsDashboard(w io.Writer, params StringsDashboardParams) error {
	return p.execute("strings/dashboard", w, params)
}

type StringTimelineParams struct {
	LoggedInUser *oauth.User
	Strings      []models.String
}

func (p *Pages) StringsTimeline(w io.Writer, params StringTimelineParams) error {
	return p.execute("strings/timeline", w, params)
}

type SingleStringParams struct {
	LoggedInUser     *oauth.User
	ShowRendered     bool
	RenderToggle     bool
	RenderedContents template.HTML
	String           models.String
	Stats            models.StringStats
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

func (p *Pages) Home(w io.Writer, params TimelineParams) error {
	return p.execute("timeline/home", w, params)
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
