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

type ResolvedRepo struct {
	models.Repo
	OwnerId    identity.Identity
	CurrentDir string
	Ref        string

	rr *RepoResolver
}

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

func (rr *RepoResolver) Resolve(r *http.Request) (*ResolvedRepo, error) {
	repo, ok := r.Context().Value("repo").(*models.Repo)
	if !ok {
		log.Println("malformed middleware: `repo` not exist in context")
		return nil, fmt.Errorf("malformed middleware")
	}
	id, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}

	currentDir := path.Dir(extractPathAfterRef(r.URL.EscapedPath()))
	ref := chi.URLParam(r, "ref")

	return &ResolvedRepo{
		Repo:       *repo,
		OwnerId:    id,
		CurrentDir: currentDir,
		Ref:        ref,

		rr: rr,
	}, nil
}

// this function is a bit weird since it now returns RepoInfo from an entirely different
// package. we should refactor this or get rid of RepoInfo entirely.
func (f *ResolvedRepo) RepoInfo(user *oauth.User) repoinfo.RepoInfo {
	repoAt := f.RepoAt()
	isStarred := false
	roles := repoinfo.RolesInRepo{}
	if user != nil {
		isStarred = db.GetStarStatus(f.rr.execer, user.Did, repoAt)
		roles.Roles = f.rr.enforcer.GetPermissionsInRepo(user.Did, f.Knot, f.DidSlashRepo())
	}

	stats := f.RepoStats
	if stats == nil {
		starCount, err := db.GetStarCount(f.rr.execer, repoAt)
		if err != nil {
			log.Println("failed to get star count for ", repoAt)
		}
		issueCount, err := db.GetIssueCount(f.rr.execer, repoAt)
		if err != nil {
			log.Println("failed to get issue count for ", repoAt)
		}
		pullCount, err := db.GetPullCount(f.rr.execer, repoAt)
		if err != nil {
			log.Println("failed to get pull count for ", repoAt)
		}
		stats = &models.RepoStats{
			StarCount:  starCount,
			IssueCount: issueCount,
			PullCount:  pullCount,
		}
	}

	sourceRepo, err := db.GetRepoSourceRepo(f.rr.execer, repoAt)
	if err != nil {
		log.Println("failed to get repo by at uri", err)
	}

	repoInfo := repoinfo.RepoInfo{
		// this is basically a models.Repo
		OwnerDid:    f.OwnerId.DID.String(),
		OwnerHandle: f.OwnerId.Handle.String(),
		Name:        f.Name,
		Rkey:        f.Rkey,
		Description: f.Description,
		Website:     f.Website,
		Topics:      f.Topics,
		Knot:        f.Knot,
		Spindle:     f.Spindle,
		Stats:       *stats,

		// fork repo upstream
		Source: sourceRepo,

		CurrentDir: f.CurrentDir,
		Ref:        f.Ref,

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
