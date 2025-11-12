package request

import (
	"context"

	"github.com/bluesky-social/indigo/atproto/identity"
	"tangled.org/core/appview/models"
)

type ctxKeyOwner struct{}
type ctxKeyRepo struct{}
type ctxKeyIssue struct{}

func WithOwner(ctx context.Context, owner *identity.Identity) context.Context {
	return context.WithValue(ctx, ctxKeyOwner{}, owner)
}

func OwnerFromContext(ctx context.Context) (*identity.Identity, bool) {
	owner, ok := ctx.Value(ctxKeyOwner{}).(*identity.Identity)
	return owner, ok
}

func WithRepo(ctx context.Context, repo *models.Repo) context.Context {
	return context.WithValue(ctx, ctxKeyRepo{}, repo)
}

func RepoFromContext(ctx context.Context) (*models.Repo, bool) {
	repo, ok := ctx.Value(ctxKeyRepo{}).(*models.Repo)
	return repo, ok
}

func WithIssue(ctx context.Context, issue *models.Issue) context.Context {
	return context.WithValue(ctx, ctxKeyIssue{}, issue)
}

func IssueFromContext(ctx context.Context) (*models.Issue, bool) {
	issue, ok := ctx.Value(ctxKeyIssue{}).(*models.Issue)
	return issue, ok
}
