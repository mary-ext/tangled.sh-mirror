package state

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sotangled/tangled/appview/auth"
	"github.com/sotangled/tangled/appview/db"
	"github.com/sotangled/tangled/appview/pages"
)

func fullyResolvedRepo(r *http.Request) (*FullyResolvedRepo, error) {
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

	// pass through values from the middleware
	description, ok := r.Context().Value("repoDescription").(string)
	addedAt, ok := r.Context().Value("repoAddedAt").(string)

	return &FullyResolvedRepo{
		Knot:        knot,
		OwnerId:     id,
		RepoName:    repoName,
		RepoAt:      parsedRepoAt,
		Description: description,
		AddedAt:     addedAt,
	}, nil
}

func RolesInRepo(s *State, u *auth.User, f *FullyResolvedRepo) pages.RolesInRepo {
	if u != nil {
		r := s.enforcer.GetPermissionsInRepo(u.Did, f.Knot, f.OwnerSlashRepo())
		return pages.RolesInRepo{r}
	} else {
		return pages.RolesInRepo{}
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

func EmailToDidOrHandle(s *State, emails []string) map[string]string {
	emailToDid, err := db.GetEmailToDid(s.db, emails)
	if err != nil {
		log.Printf("error fetching dids for emails: %v", err)
		return nil
	}

	log.Println(emailToDid)
	var dids []string
	for _, v := range emailToDid {
		dids = append(dids, v)
	}
	log.Println(dids)
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

	log.Println(emailToDidOrHandle)

	return emailToDidOrHandle
}
