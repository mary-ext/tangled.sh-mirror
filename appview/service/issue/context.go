package issue

import (
	"context"

	"tangled.org/core/appview/models"
)

type ctxKey struct{}

func IntoContext(ctx context.Context, repo *models.Issue) context.Context {
	return context.WithValue(ctx, ctxKey{}, repo)
}

func FromContext(ctx context.Context) (*models.Issue, bool) {
	repo, ok := ctx.Value(ctxKey{}).(*models.Issue)
	return repo, ok
}
