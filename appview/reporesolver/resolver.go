package reporesolver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pages/repoinfo"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/resolver"
)

type ResolvedRepo struct {
	Knot        string
	OwnerId     identity.Identity
	RepoName    string
	RepoAt      syntax.ATURI
	Description string
	CreatedAt   string
	Ref         string
	CurrentDir  string

	rr *RepoResolver
}

type RepoResolver struct {
	config   *appview.Config
	enforcer *rbac.Enforcer
	resolver *resolver.Resolver
	execer   db.Execer
}

func New(config *appview.Config, enforcer *rbac.Enforcer, resolver *resolver.Resolver, execer db.Execer) *RepoResolver {
	return &RepoResolver{config: config, enforcer: enforcer, resolver: resolver, execer: execer}
}

func (rr *RepoResolver) Resolve(r *http.Request) (*ResolvedRepo, error) {
	repoName := chi.URLParam(r, "repo")
	knot, ok := r.Context().Value("knot").(string)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}
	id, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}

	repoAt, ok := r.Context().Value("repoAt").(string)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}

	parsedRepoAt, err := syntax.ParseATURI(repoAt)
	if err != nil {
		log.Println("malformed repo at-uri")
		return nil, fmt.Errorf("malformed middleware")
	}

	ref := chi.URLParam(r, "ref")

	if ref == "" {
		us, err := knotclient.NewUnsignedClient(knot, rr.config.Core.Dev)
		if err != nil {
			return nil, err
		}

		defaultBranch, err := us.DefaultBranch(id.DID.String(), repoName)
		if err != nil {
			return nil, err
		}

		ref = defaultBranch.Branch
	}

	currentDir := path.Dir(extractPathAfterRef(r.URL.EscapedPath(), ref))

	// pass through values from the middleware
	description, ok := r.Context().Value("repoDescription").(string)
	addedAt, ok := r.Context().Value("repoAddedAt").(string)

	return &ResolvedRepo{
		Knot:        knot,
		OwnerId:     id,
		RepoName:    repoName,
		RepoAt:      parsedRepoAt,
		Description: description,
		CreatedAt:   addedAt,
		Ref:         ref,
		CurrentDir:  currentDir,

		rr: rr,
	}, nil
}

func (f *ResolvedRepo) OwnerDid() string {
	return f.OwnerId.DID.String()
}

func (f *ResolvedRepo) OwnerHandle() string {
	return f.OwnerId.Handle.String()
}

func (f *ResolvedRepo) OwnerSlashRepo() string {
	handle := f.OwnerId.Handle

	var p string
	if handle != "" && !handle.IsInvalidHandle() {
		p, _ = securejoin.SecureJoin(fmt.Sprintf("@%s", handle), f.RepoName)
	} else {
		p, _ = securejoin.SecureJoin(f.OwnerDid(), f.RepoName)
	}

	return p
}

func (f *ResolvedRepo) DidSlashRepo() string {
	p, _ := securejoin.SecureJoin(f.OwnerDid(), f.RepoName)
	return p
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
		if item[3] == "repo:owner" {
			role = "owner"
		} else if item[3] == "repo:collaborator" {
			role = "collaborator"
		} else {
			continue
		}

		did := item[0]

		c := pages.Collaborator{
			Did:    did,
			Handle: "",
			Role:   role,
		}
		collaborators = append(collaborators, c)
	}

	// populate all collborators with handles
	identsToResolve := make([]string, len(collaborators))
	for i, collab := range collaborators {
		identsToResolve[i] = collab.Did
	}

	resolvedIdents := f.rr.resolver.ResolveIdents(ctx, identsToResolve)
	for i, resolved := range resolvedIdents {
		if resolved != nil {
			collaborators[i].Handle = resolved.Handle.String()
		}
	}

	return collaborators, nil
}

// this function is a bit weird since it now returns RepoInfo from an entirely different
// package. we should refactor this or get rid of RepoInfo entirely.
func (f *ResolvedRepo) RepoInfo(user *oauth.User) repoinfo.RepoInfo {
	isStarred := false
	if user != nil {
		isStarred = db.GetStarStatus(f.rr.execer, user.Did, syntax.ATURI(f.RepoAt))
	}

	starCount, err := db.GetStarCount(f.rr.execer, f.RepoAt)
	if err != nil {
		log.Println("failed to get star count for ", f.RepoAt)
	}
	issueCount, err := db.GetIssueCount(f.rr.execer, f.RepoAt)
	if err != nil {
		log.Println("failed to get issue count for ", f.RepoAt)
	}
	pullCount, err := db.GetPullCount(f.rr.execer, f.RepoAt)
	if err != nil {
		log.Println("failed to get issue count for ", f.RepoAt)
	}
	source, err := db.GetRepoSource(f.rr.execer, f.RepoAt)
	if errors.Is(err, sql.ErrNoRows) {
		source = ""
	} else if err != nil {
		log.Println("failed to get repo source for ", f.RepoAt, err)
	}

	var sourceRepo *db.Repo
	if source != "" {
		sourceRepo, err = db.GetRepoByAtUri(f.rr.execer, source)
		if err != nil {
			log.Println("failed to get repo by at uri", err)
		}
	}

	var sourceHandle *identity.Identity
	if sourceRepo != nil {
		sourceHandle, err = f.rr.resolver.ResolveIdent(context.Background(), sourceRepo.Did)
		if err != nil {
			log.Println("failed to resolve source repo", err)
		}
	}

	knot := f.Knot
	var disableFork bool
	us, err := knotclient.NewUnsignedClient(knot, f.rr.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s: %v", knot, err)
	} else {
		result, err := us.Branches(f.OwnerDid(), f.RepoName)
		if err != nil {
			log.Printf("failed to get branches for %s/%s: %v", f.OwnerDid(), f.RepoName, err)
		}

		if len(result.Branches) == 0 {
			disableFork = true
		}
	}

	repoInfo := repoinfo.RepoInfo{
		OwnerDid:    f.OwnerDid(),
		OwnerHandle: f.OwnerHandle(),
		Name:        f.RepoName,
		RepoAt:      f.RepoAt,
		Description: f.Description,
		Ref:         f.Ref,
		IsStarred:   isStarred,
		Knot:        knot,
		Roles:       f.RolesInRepo(user),
		Stats: db.RepoStats{
			StarCount:  starCount,
			IssueCount: issueCount,
			PullCount:  pullCount,
		},
		DisableFork: disableFork,
		CurrentDir:  f.CurrentDir,
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
		return repoinfo.RolesInRepo{r}
	} else {
		return repoinfo.RolesInRepo{}
	}
}

// extractPathAfterRef gets the actual repository path
// after the ref. for example:
//
//	/@icyphox.sh/foorepo/blob/main/abc/xyz/ => abc/xyz/
func extractPathAfterRef(fullPath, ref string) string {
	fullPath = strings.TrimPrefix(fullPath, "/")

	ref = url.PathEscape(ref)

	prefixes := []string{
		fmt.Sprintf("blob/%s/", ref),
		fmt.Sprintf("tree/%s/", ref),
		fmt.Sprintf("raw/%s/", ref),
	}

	for _, prefix := range prefixes {
		idx := strings.Index(fullPath, prefix)
		if idx != -1 {
			return fullPath[idx+len(prefix):]
		}
	}

	return ""
}
