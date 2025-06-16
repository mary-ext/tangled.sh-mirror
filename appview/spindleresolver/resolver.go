package spindleresolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/cache"
	"tangled.sh/tangled.sh/core/appview/idresolver"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
)

type ResolutionStatus string

const (
	StatusOK      ResolutionStatus = "ok"
	StatusError   ResolutionStatus = "error"
	StatusInvalid ResolutionStatus = "invalid"
)

type Resolution struct {
	Status     ResolutionStatus `json:"status"`
	OwnerDID   string           `json:"ownerDid,omitempty"`
	VerifiedAt time.Time        `json:"verifiedAt"`
}

type Resolver struct {
	cache      *cache.Cache
	http       *http.Client
	config     Config
	idResolver *idresolver.Resolver
}

type Config struct {
	HitTTL     time.Duration
	ErrTTL     time.Duration
	InvalidTTL time.Duration
	Dev        bool
}

func NewResolver(cache *cache.Cache, client *http.Client, config Config) *Resolver {
	if client == nil {
		client = &http.Client{
			Timeout: 2 * time.Second,
		}
	}
	return &Resolver{
		cache:  cache,
		http:   client,
		config: config,
	}
}

func DefaultResolver(cache *cache.Cache) *Resolver {
	return NewResolver(
		cache,
		&http.Client{
			Timeout: 2 * time.Second,
		},
		Config{
			HitTTL:     24 * time.Hour,
			ErrTTL:     30 * time.Second,
			InvalidTTL: 1 * time.Minute,
		},
	)
}

func (r *Resolver) ResolveInstance(ctx context.Context, domain string) (*Resolution, error) {
	key := "spindle:" + domain

	val, err := r.cache.Get(ctx, key).Result()
	if err == nil {
		var cached Resolution
		if err := json.Unmarshal([]byte(val), &cached); err == nil {
			return &cached, nil
		}
	}

	resolution, ttl := r.verify(ctx, domain)

	data, _ := json.Marshal(resolution)
	r.cache.Set(ctx, key, data, ttl)

	if resolution.Status == StatusOK {
		return resolution, nil
	}

	return resolution, fmt.Errorf("verification failed: %s", resolution.Status)
}

func (r *Resolver) verify(ctx context.Context, domain string) (*Resolution, time.Duration) {
	owner, err := r.fetchOwner(ctx, domain)
	if err != nil {
		return &Resolution{Status: StatusError, VerifiedAt: time.Now()}, r.config.ErrTTL
	}

	record, err := r.fetchRecord(ctx, owner, domain)
	if err != nil {
		return &Resolution{Status: StatusError, VerifiedAt: time.Now()}, r.config.ErrTTL
	}

	if record.Instance == domain {
		return &Resolution{
			Status:     StatusOK,
			OwnerDID:   owner,
			VerifiedAt: time.Now(),
		}, r.config.HitTTL
	}

	return &Resolution{
		Status:     StatusInvalid,
		OwnerDID:   owner,
		VerifiedAt: time.Now(),
	}, r.config.InvalidTTL
}

func (r *Resolver) fetchOwner(ctx context.Context, domain string) (string, error) {
	scheme := "https"
	if r.config.Dev {
		scheme = "http"
	}

	url := fmt.Sprintf("%s://%s/owner", scheme, domain)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := r.http.Do(req.WithContext(ctx))
	if err != nil || resp.StatusCode != 200 {
		return "", errors.New("failed to fetch /owner")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024)) // read atmost 1kb of data
	if err != nil {
		return "", fmt.Errorf("failed to read /owner response: %w", err)
	}

	did := strings.TrimSpace(string(body))
	if did == "" {
		return "", errors.New("empty DID in /owner response")
	}

	return did, nil
}

func (r *Resolver) fetchRecord(ctx context.Context, did, rkey string) (*tangled.Spindle, error) {
	ident, err := r.idResolver.ResolveIdent(ctx, did)
	if err != nil {
		return nil, err
	}

	xrpcc := xrpc.Client{
		Host: ident.PDSEndpoint(),
	}

	rec, err := atproto.RepoGetRecord(ctx, &xrpcc, "", tangled.SpindleNSID, did, rkey)
	if err != nil {
		return nil, err
	}

	out, ok := rec.Value.Val.(*tangled.Spindle)
	if !ok {
		return nil, fmt.Errorf("invalid record returned")
	}

	return out, nil
}
