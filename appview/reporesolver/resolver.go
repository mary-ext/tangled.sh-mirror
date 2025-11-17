package reporesolver

import (
	"fmt"
	"log"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages/repoinfo"
	"tangled.org/core/rbac"
)

type RepoResolver struct {
	config   *config.Config
	enforcer *rbac.Enforcer
	execer   db.Execer
}

func New(config *config.Config, enforcer *rbac.Enforcer, execer db.Execer) *RepoResolver {
	return &RepoResolver{config: config, enforcer: enforcer, execer: execer}
}

// NOTE: this... should not even be here. the entire package will be removed in future refactor
func GetBaseRepoPath(r *http.Request, repo *models.Repo) string {
	var (
		user = chi.URLParam(r, "user")
		name = chi.URLParam(r, "repo")
	)
	if user == "" || name == "" {
		return repo.DidSlashRepo()
	}
	return path.Join(user, name)
}

// TODO: move this out of `RepoResolver` struct
func (rr *RepoResolver) Resolve(r *http.Request) (*models.Repo, error) {
	repo, ok := r.Context().Value("repo").(*models.Repo)
	if !ok {
		log.Println("malformed middleware: `repo` not exist in context")
		return nil, fmt.Errorf("malformed middleware")
	}

	return repo, nil
}

// 1. [x] replace `RepoInfo` to `reporesolver.GetRepoInfo(r *http.Request, repo, user)`
// 2. [x] remove `rr`, `CurrentDir`, `Ref` fields from `ResolvedRepo`
// 3. [ ] remove `ResolvedRepo`
// 4. [ ] replace reporesolver to reposervice
func (rr *RepoResolver) GetRepoInfo(r *http.Request, user *oauth.User) repoinfo.RepoInfo {
	ownerId, ook := r.Context().Value("resolvedId").(identity.Identity)
	repo, rok := r.Context().Value("repo").(*models.Repo)
	if !ook || !rok {
		log.Println("malformed request, failed to get repo from context")
	}

	// get dir/ref
	currentDir := path.Dir(extractPathAfterRef(r.URL.EscapedPath()))
	ref := chi.URLParam(r, "ref")

	repoAt := repo.RepoAt()
	isStarred := false
	roles := repoinfo.RolesInRepo{}
	if user != nil {
		isStarred = db.GetStarStatus(rr.execer, user.Did, repoAt)
		roles.Roles = rr.enforcer.GetPermissionsInRepo(user.Did, repo.Knot, repo.DidSlashRepo())
	}

	stats := repo.RepoStats
	if stats == nil {
		starCount, err := db.GetStarCount(rr.execer, repoAt)
		if err != nil {
			log.Println("failed to get star count for ", repoAt)
		}
		issueCount, err := db.GetIssueCount(rr.execer, repoAt)
		if err != nil {
			log.Println("failed to get issue count for ", repoAt)
		}
		pullCount, err := db.GetPullCount(rr.execer, repoAt)
		if err != nil {
			log.Println("failed to get pull count for ", repoAt)
		}
		stats = &models.RepoStats{
			StarCount:  starCount,
			IssueCount: issueCount,
			PullCount:  pullCount,
		}
	}

	var sourceRepo *models.Repo
	var err error
	if repo.Source != "" {
		sourceRepo, err = db.GetRepoByAtUri(rr.execer, repo.Source)
		if err != nil {
			log.Println("failed to get repo by at uri", err)
		}
	}

	repoInfo := repoinfo.RepoInfo{
		// this is basically a models.Repo
		OwnerDid:    ownerId.DID.String(),
		OwnerHandle: ownerId.Handle.String(),
		Name:        repo.Name,
		Rkey:        repo.Rkey,
		Description: repo.Description,
		Website:     repo.Website,
		Topics:      repo.Topics,
		Knot:        repo.Knot,
		Spindle:     repo.Spindle,
		Stats:       *stats,

		// fork repo upstream
		Source: sourceRepo,

		// page context
		CurrentDir: currentDir,
		Ref:        ref,

		// info related to the session
		IsStarred: isStarred,
		Roles:     roles,
	}

	return repoInfo
}

// extractPathAfterRef gets the actual repository path
// after the ref. for example:
//
//	/@icyphox.sh/foorepo/blob/main/abc/xyz/ => abc/xyz/
func extractPathAfterRef(fullPath string) string {
	fullPath = strings.TrimPrefix(fullPath, "/")

	// match blob/, tree/, or raw/ followed by any ref and then a slash
	//
	// captures everything after the final slash
	pattern := `(?:blob|tree|raw)/[^/]+/(.*)$`

	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(fullPath)

	if len(matches) > 1 {
		return matches[1]
	}

	return ""
}
