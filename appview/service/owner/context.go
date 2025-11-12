package owner

import (
	"context"

	"github.com/bluesky-social/indigo/atproto/identity"
)

type ctxKey struct{}

func IntoContext(ctx context.Context, id *identity.Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func FromContext(ctx context.Context) (*identity.Identity, bool) {
	repo, ok := ctx.Value(ctxKey{}).(*identity.Identity)
	return repo, ok
}
