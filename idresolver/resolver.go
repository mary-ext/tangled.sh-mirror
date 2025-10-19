package idresolver

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/identity/redisdir"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/carlmjohnson/versioninfo"
)

type Resolver struct {
	directory identity.Directory
}

func BaseDirectory(plcUrl string) identity.Directory {
	base := identity.BaseDirectory{
		PLCURL: plcUrl,
		HTTPClient: http.Client{
			Timeout: time.Second * 10,
			Transport: &http.Transport{
				// would want this around 100ms for services doing lots of handle resolution. Impacts PLC connections as well, but not too bad.
				IdleConnTimeout: time.Millisecond * 1000,
				MaxIdleConns:    100,
			},
		},
		Resolver: net.Resolver{
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: time.Second * 3}
				return d.DialContext(ctx, network, address)
			},
		},
		TryAuthoritativeDNS: true,
		// primary Bluesky PDS instance only supports HTTP resolution method
		SkipDNSDomainSuffixes: []string{".bsky.social"},
		UserAgent:             "indigo-identity/" + versioninfo.Short(),
	}
	return &base
}

func RedisDirectory(url, plcUrl string) (identity.Directory, error) {
	hitTTL := time.Hour * 24
	errTTL := time.Second * 30
	invalidHandleTTL := time.Minute * 5
	return redisdir.NewRedisDirectory(
		BaseDirectory(plcUrl),
		url,
		hitTTL,
		errTTL,
		invalidHandleTTL,
		10000,
	)
}

func DefaultResolver(plcUrl string) *Resolver {
	base := BaseDirectory(plcUrl)
	cached := identity.NewCacheDirectory(base, 250_000, time.Hour*24, time.Minute*2, time.Minute*5)
	return &Resolver{
		directory: &cached,
	}
}

func RedisResolver(redisUrl, plcUrl string) (*Resolver, error) {
	directory, err := RedisDirectory(redisUrl, plcUrl)
	if err != nil {
		return nil, err
	}
	return &Resolver{
		directory: directory,
	}, nil
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

func (r *Resolver) InvalidateIdent(ctx context.Context, arg string) error {
	id, err := syntax.ParseAtIdentifier(arg)
	if err != nil {
		return err
	}

	return r.directory.Purge(ctx, *id)
}

func (r *Resolver) Directory() identity.Directory {
	return r.directory
}
