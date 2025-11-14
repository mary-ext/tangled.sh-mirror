package reporesolver

import (
	"context"
	"database/sql"
	"errors"
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
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pages/repoinfo"
	"tangled.org/core/idresolver"
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
	config     *config.Config
	enforcer   *rbac.Enforcer
	idResolver *idresolver.Resolver
	execer     db.Execer
}

func New(config *config.Config, enforcer *rbac.Enforcer, resolver *idresolver.Resolver, execer db.Execer) *RepoResolver {
	return &RepoResolver{config: config, enforcer: enforcer, idResolver: resolver, execer: execer}
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

func (f *ResolvedRepo) Collaborators(ctx context.Context) ([]pages.Collaborator, error) {
	repoCollaborators, err := f.rr.enforcer.E.GetImplicitUsersForResourceByDomain(f.DidSlashRepo(), f.Knot)
	if err != nil {
		return nil, err
	}

	var collaborators []pages.Collaborator
	for _, item := range repoCollaborators {
		// currently only two roles: owner and member
		var role string
		switch item[3] {
		case "repo:owner":
			role = "owner"
		case "repo:collaborator":
			role = "collaborator"
		default:
			continue
		}

		did := item[0]

		c := pages.Collaborator{
			Did:    did,
			Role:   role,
		}
		collaborators = append(collaborators, c)
	}

	return collaborators, nil
}

// this function is a bit weird since it now returns RepoInfo from an entirely different
// package. we should refactor this or get rid of RepoInfo entirely.
func (f *ResolvedRepo) RepoInfo(user *oauth.User) repoinfo.RepoInfo {
	repoAt := f.RepoAt()
	isStarred := false
	if user != nil {
		isStarred = db.GetStarStatus(f.rr.execer, user.Did, repoAt)
	}

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
		log.Println("failed to get issue count for ", repoAt)
	}
	source, err := db.GetRepoSource(f.rr.execer, repoAt)
	if errors.Is(err, sql.ErrNoRows) {
		source = ""
	} else if err != nil {
		log.Println("failed to get repo source for ", repoAt, err)
	}

	var sourceRepo *models.Repo
	if source != "" {
		sourceRepo, err = db.GetRepoByAtUri(f.rr.execer, source)
		if err != nil {
			log.Println("failed to get repo by at uri", err)
		}
	}

	var sourceHandle *identity.Identity
	if sourceRepo != nil {
		sourceHandle, err = f.rr.idResolver.ResolveIdent(context.Background(), sourceRepo.Did)
		if err != nil {
			log.Println("failed to resolve source repo", err)
		}
	}

	knot := f.Knot

	repoInfo := repoinfo.RepoInfo{
		OwnerDid:    f.OwnerId.DID.String(),
		OwnerHandle: f.OwnerId.Handle.String(),
		Name:        f.Name,
		Rkey:        f.Rkey,
		RepoAt:      repoAt,
		Description: f.Description,
		Website:     f.Website,
		Topics:      f.Topics,
		IsStarred:   isStarred,
		Knot:        knot,
		Spindle:     f.Spindle,
		Roles:       f.RolesInRepo(user),
		Stats: models.RepoStats{
			StarCount:  starCount,
			IssueCount: issueCount,
			PullCount:  pullCount,
		},
		CurrentDir: f.CurrentDir,
		Ref:        f.Ref,
	}

	if sourceRepo != nil {
		repoInfo.Source = sourceRepo
		repoInfo.SourceHandle = sourceHandle.Handle.String()
	}

	return repoInfo
}

func (f *ResolvedRepo) RolesInRepo(u *oauth.User) repoinfo.RolesInRepo {
	if u != nil {
		r := f.rr.enforcer.GetPermissionsInRepo(u.Did, f.Knot, f.DidSlashRepo())
		return repoinfo.RolesInRepo{Roles: r}
	} else {
		return repoinfo.RolesInRepo{}
	}
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
