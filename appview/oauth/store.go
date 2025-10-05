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

// redis-backed implementation of ClientAuthStore.
type RedisStore struct {
	client         *redis.Client
	SessionTTL     time.Duration
	AuthRequestTTL time.Duration
}

var _ oauth.ClientAuthStore = &RedisStore{}

func NewRedisStore(redisURL string) (*RedisStore, error) {
	opts, err := redis.ParseURL(redisURL)
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
		client:         client,
		SessionTTL:     30 * 24 * time.Hour, // 30 days
		AuthRequestTTL: 10 * time.Minute,    // 10 minutes
	}, nil
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}

func sessionKey(did syntax.DID, sessionID string) string {
	return fmt.Sprintf("oauth:session:%s:%s", did, sessionID)
}

func authRequestKey(state string) string {
	return fmt.Sprintf("oauth:auth_request:%s", state)
}

func (r *RedisStore) GetSession(ctx context.Context, did syntax.DID, sessionID string) (*oauth.ClientSessionData, error) {
	key := sessionKey(did, sessionID)
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

	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	if err := r.client.Set(ctx, key, data, r.SessionTTL).Err(); err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	return nil
}

func (r *RedisStore) DeleteSession(ctx context.Context, did syntax.DID, sessionID string) error {
	key := sessionKey(did, sessionID)
	if err := r.client.Del(ctx, key).Err(); err != nil {
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

	if err := r.client.Set(ctx, key, data, r.AuthRequestTTL).Err(); err != nil {
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
