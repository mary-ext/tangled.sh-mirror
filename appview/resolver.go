package appview

import (
	"context"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Resolver struct {
	directory identity.Directory
}

func NewResolver() *Resolver {
	return &Resolver{
		directory: identity.DefaultDirectory(),
	}
}

func (r *Resolver) ResolveIdent(ctx context.Context, arg string) (*identity.Identity, error) {
	id, err := syntax.ParseAtIdentifier(arg)
	if err != nil {
		return nil, err
	}

	return r.directory.Lookup(ctx, *id)
}
