package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/redis/go-redis/v9"
)

type RedisStoreConfig struct {
	RedisURL string

	// The purpose of these limits is to avoid dead sessions hanging around in the db indefinitely.
	// The durations here should be *at least as long as* the expected duration of the oauth session itself.
	SessionExpiryDuration     time.Duration // duration since session creation (max TTL)
	SessionInactivityDuration time.Duration // duration since last session update
	AuthRequestExpiryDuration time.Duration // duration since auth request creation
}

// redis-backed implementation of ClientAuthStore.
type RedisStore struct {
	client *redis.Client
	cfg    *RedisStoreConfig
}

var _ oauth.ClientAuthStore = &RedisStore{}

type sessionMetadata struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewRedisStore(cfg *RedisStoreConfig) (*RedisStore, error) {
	if cfg == nil {
		return nil, fmt.Errorf("missing cfg")
	}
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("missing RedisURL")
	}
	if cfg.SessionExpiryDuration == 0 {
		return nil, fmt.Errorf("missing SessionExpiryDuration")
	}
	if cfg.SessionInactivityDuration == 0 {
		return nil, fmt.Errorf("missing SessionInactivityDuration")
	}
	if cfg.AuthRequestExpiryDuration == 0 {
		return nil, fmt.Errorf("missing AuthRequestExpiryDuration")
	}

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis URL: %w", err)
	}

	client := redis.NewClient(opts)

	// test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &RedisStore{
		client: client,
		cfg:    cfg,
	}, nil
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}

func sessionKey(did syntax.DID, sessionID string) string {
	return fmt.Sprintf("oauth:session:%s:%s", did, sessionID)
}

func sessionMetadataKey(did syntax.DID, sessionID string) string {
	return fmt.Sprintf("oauth:session_meta:%s:%s", did, sessionID)
}

func authRequestKey(state string) string {
	return fmt.Sprintf("oauth:auth_request:%s", state)
}

func (r *RedisStore) GetSession(ctx context.Context, did syntax.DID, sessionID string) (*oauth.ClientSessionData, error) {
	key := sessionKey(did, sessionID)
	metaKey := sessionMetadataKey(did, sessionID)

	// Check metadata for inactivity expiry
	metaData, err := r.client.Get(ctx, metaKey).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("session not found: %s", did)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session metadata: %w", err)
	}

	var meta sessionMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session metadata: %w", err)
	}

	// Check if session has been inactive for too long
	inactiveThreshold := time.Now().Add(-r.cfg.SessionInactivityDuration)
	if meta.UpdatedAt.Before(inactiveThreshold) {
		// Session is inactive, delete it
		r.client.Del(ctx, key, metaKey)
		return nil, fmt.Errorf("session expired due to inactivity: %s", did)
	}

	// Get the actual session data
	data, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("session not found: %s", did)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	var sess oauth.ClientSessionData
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session: %w", err)
	}

	return &sess, nil
}

func (r *RedisStore) SaveSession(ctx context.Context, sess oauth.ClientSessionData) error {
	key := sessionKey(sess.AccountDID, sess.SessionID)
	metaKey := sessionMetadataKey(sess.AccountDID, sess.SessionID)

	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	// Check if session already exists to preserve CreatedAt
	var meta sessionMetadata
	existingMetaData, err := r.client.Get(ctx, metaKey).Bytes()
	if err == redis.Nil {
		// New session
		meta = sessionMetadata{
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
	} else if err != nil {
		return fmt.Errorf("failed to check existing session metadata: %w", err)
	} else {
		// Existing session - preserve CreatedAt, update UpdatedAt
		if err := json.Unmarshal(existingMetaData, &meta); err != nil {
			return fmt.Errorf("failed to unmarshal existing session metadata: %w", err)
		}
		meta.UpdatedAt = time.Now()
	}

	// Calculate remaining TTL based on creation time
	remainingTTL := r.cfg.SessionExpiryDuration - time.Since(meta.CreatedAt)
	if remainingTTL <= 0 {
		return fmt.Errorf("session has expired")
	}

	// Use the shorter of: remaining TTL or inactivity duration
	ttl := min(r.cfg.SessionInactivityDuration, remainingTTL)

	// Save session data
	if err := r.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	// Save metadata
	metaData, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal session metadata: %w", err)
	}
	if err := r.client.Set(ctx, metaKey, metaData, ttl).Err(); err != nil {
		return fmt.Errorf("failed to save session metadata: %w", err)
	}

	return nil
}

func (r *RedisStore) DeleteSession(ctx context.Context, did syntax.DID, sessionID string) error {
	key := sessionKey(did, sessionID)
	metaKey := sessionMetadataKey(did, sessionID)

	if err := r.client.Del(ctx, key, metaKey).Err(); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

func (r *RedisStore) GetAuthRequestInfo(ctx context.Context, state string) (*oauth.AuthRequestData, error) {
	key := authRequestKey(state)
	data, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("request info not found: %s", state)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get auth request: %w", err)
	}

	var req oauth.AuthRequestData
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal auth request: %w", err)
	}

	return &req, nil
}

func (r *RedisStore) SaveAuthRequestInfo(ctx context.Context, info oauth.AuthRequestData) error {
	key := authRequestKey(info.State)

	// check if already exists (to match MemStore behavior)
	exists, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("failed to check auth request existence: %w", err)
	}
	if exists > 0 {
		return fmt.Errorf("auth request already saved for state %s", info.State)
	}

	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}

	if err := r.client.Set(ctx, key, data, r.cfg.AuthRequestExpiryDuration).Err(); err != nil {
		return fmt.Errorf("failed to save auth request: %w", err)
	}

	return nil
}

func (r *RedisStore) DeleteAuthRequestInfo(ctx context.Context, state string) error {
	key := authRequestKey(state)
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete auth request: %w", err)
	}
	return nil
}
