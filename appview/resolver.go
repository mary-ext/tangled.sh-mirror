package appview

import (
	"context"
	"sync"

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

func (r *Resolver) ResolveIdents(ctx context.Context, idents []string) []*identity.Identity {
	results := make([]*identity.Identity, len(idents))
	var wg sync.WaitGroup

	done := make(chan struct{})
	defer close(done)

	for idx, ident := range idents {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				results[index] = nil
			case <-done:
				results[index] = nil
			default:
				identity, _ := r.ResolveIdent(ctx, id)
				results[index] = identity
			}
		}(idx, ident)
	}

	wg.Wait()
	return results
}
