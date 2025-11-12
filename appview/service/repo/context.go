package repo

import (
	"context"

	"tangled.org/core/appview/models"
)

type ctxKey struct{}

func IntoContext(ctx context.Context, repo *models.Repo) context.Context {
	return context.WithValue(ctx, ctxKey{}, repo)
}

func FromContext(ctx context.Context) (*models.Repo, bool) {
	repo, ok := ctx.Value(ctxKey{}).(*models.Repo)
	return repo, ok
}
