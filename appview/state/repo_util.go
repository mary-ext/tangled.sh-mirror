package state

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"go.opentelemetry.io/otel/attribute"
	"tangled.sh/tangled.sh/core/appview/auth"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages/repoinfo"
	"tangled.sh/tangled.sh/core/telemetry"
)

func (s *State) fullyResolvedRepo(r *http.Request) (*FullyResolvedRepo, error) {
	ctx := r.Context()

	attrs := telemetry.MapAttrs(map[string]string{
		"repo": chi.URLParam(r, "repo"),
		"ref":  chi.URLParam(r, "ref"),
	})

	ctx, span := s.t.TraceStart(ctx, "fullyResolvedRepo", attrs...)
	defer span.End()

	repoName := chi.URLParam(r, "repo")
	knot, ok := ctx.Value("knot").(string)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}

	span.SetAttributes(attribute.String("knot", knot))

	id, ok := ctx.Value("resolvedId").(identity.Identity)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}

	span.SetAttributes(attribute.String("did", id.DID.String()))

	repoAt, ok := ctx.Value("repoAt").(string)
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
		us, err := NewUnsignedClient(knot, s.config.Dev)
		if err != nil {
			return nil, err
		}

		defaultBranch, err := us.DefaultBranch(id.DID.String(), repoName)
		if err != nil {
			return nil, err
		}

		ref = defaultBranch.Branch

		span.SetAttributes(attribute.String("default_branch", ref))
	}

	description, ok := ctx.Value("repoDescription").(string)
	addedAt, ok := ctx.Value("repoAddedAt").(string)

	return &FullyResolvedRepo{
		Knot:        knot,
		OwnerId:     id,
		RepoName:    repoName,
		RepoAt:      parsedRepoAt,
		Description: description,
		CreatedAt:   addedAt,
		Ref:         ref,
	}, nil
}

func RolesInRepo(s *State, u *auth.User, f *FullyResolvedRepo) repoinfo.RolesInRepo {
	if u != nil {
		r := s.enforcer.GetPermissionsInRepo(u.Did, f.Knot, f.DidSlashRepo())
		return repoinfo.RolesInRepo{r}
	} else {
		return repoinfo.RolesInRepo{}
	}
}

func uniqueEmails(commits []*object.Commit) []string {
	emails := make(map[string]struct{})
	for _, commit := range commits {
		if commit.Author.Email != "" {
			emails[commit.Author.Email] = struct{}{}
		}
		if commit.Committer.Email != "" {
			emails[commit.Committer.Email] = struct{}{}
		}
	}
	var uniqueEmails []string
	for email := range emails {
		uniqueEmails = append(uniqueEmails, email)
	}
	return uniqueEmails
}

func balanceIndexItems(commitCount, branchCount, tagCount, fileCount int) (commitsTrunc int, branchesTrunc int, tagsTrunc int) {
	if commitCount == 0 && tagCount == 0 && branchCount == 0 {
		return
	}

	// typically 1 item on right side = 2 files in height
	availableSpace := fileCount / 2

	// clamp tagcount
	if tagCount > 0 {
		tagsTrunc = 1
		availableSpace -= 1 // an extra subtracted for headers etc.
	}

	// clamp branchcount
	if branchCount > 0 {
		branchesTrunc = min(max(branchCount, 1), 2)
		availableSpace -= branchesTrunc // an extra subtracted for headers etc.
	}

	// show
	if commitCount > 0 {
		commitsTrunc = max(availableSpace, 3)
	}

	return
}

func EmailToDidOrHandle(s *State, emails []string) map[string]string {
	emailToDid, err := db.GetEmailToDid(s.db, emails, true) // only get verified emails for mapping
	if err != nil {
		log.Printf("error fetching dids for emails: %v", err)
		return nil
	}

	var dids []string
	for _, v := range emailToDid {
		dids = append(dids, v)
	}
	resolvedIdents := s.resolver.ResolveIdents(context.Background(), dids)

	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIdents {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	// Create map of email to didOrHandle for commit display
	emailToDidOrHandle := make(map[string]string)
	for email, did := range emailToDid {
		if didOrHandle, ok := didHandleMap[did]; ok {
			emailToDidOrHandle[email] = didOrHandle
		}
	}

	return emailToDidOrHandle
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	result := make([]byte, n)

	for i := 0; i < n; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		result[i] = letters[n.Int64()]
	}

	return string(result)
}
