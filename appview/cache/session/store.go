package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tangled.sh/tangled.sh/core/appview/cache"
)

type OAuthSession struct {
	Handle              string
	Did                 string
	PdsUrl              string
	AccessJwt           string
	RefreshJwt          string
	AuthServerIss       string
	DpopPdsNonce        string
	DpopAuthserverNonce string
	DpopPrivateJwk      string
	Expiry              string
}

type OAuthRequest struct {
	AuthserverIss       string
	Handle              string
	State               string
	Did                 string
	PdsUrl              string
	PkceVerifier        string
	DpopAuthserverNonce string
	DpopPrivateJwk      string
}

type SessionStore struct {
	cache *cache.Cache
}

const (
	stateKey   = "oauthstate:%s"
	requestKey = "oauthrequest:%s"
	sessionKey = "oauthsession:%s"
)

func New(cache *cache.Cache) *SessionStore {
	return &SessionStore{cache: cache}
}

func (s *SessionStore) SaveSession(ctx context.Context, session OAuthSession) error {
	key := fmt.Sprintf(sessionKey, session.Did)
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}

	// set with ttl (expires in + buffer)
	expiry, _ := time.Parse(time.RFC3339, session.Expiry)
	ttl := time.Until(expiry) + time.Minute

	return s.cache.Set(ctx, key, data, ttl).Err()
}

// SaveRequest stores the OAuth request to be later fetched in the callback. Since
// the fetching happens by comparing the state we get in the callback params, we
// store an additional state->did mapping which then lets us fetch the whole OAuth request.
func (s *SessionStore) SaveRequest(ctx context.Context, request OAuthRequest) error {
	key := fmt.Sprintf(requestKey, request.Did)
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}

	// oauth flow must complete within 30 minutes
	err = s.cache.Set(ctx, key, data, 30*time.Minute).Err()
	if err != nil {
		return fmt.Errorf("error saving request: %w", err)
	}

	stateKey := fmt.Sprintf(stateKey, request.State)
	err = s.cache.Set(ctx, stateKey, request.Did, 30*time.Minute).Err()
	if err != nil {
		return fmt.Errorf("error saving state->did mapping: %w", err)
	}

	return nil
}

func (s *SessionStore) GetSession(ctx context.Context, did string) (*OAuthSession, error) {
	key := fmt.Sprintf(sessionKey, did)
	val, err := s.cache.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var session OAuthSession
	err = json.Unmarshal([]byte(val), &session)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *SessionStore) GetRequestByState(ctx context.Context, state string) (*OAuthRequest, error) {
	didKey, err := s.getRequestKeyFromState(ctx, state)
	if err != nil {
		return nil, err
	}

	val, err := s.cache.Get(ctx, didKey).Result()
	if err != nil {
		return nil, err
	}

	var request OAuthRequest
	err = json.Unmarshal([]byte(val), &request)
	if err != nil {
		return nil, err
	}

	return &request, nil
}

func (s *SessionStore) DeleteSession(ctx context.Context, did string) error {
	key := fmt.Sprintf(sessionKey, did)
	return s.cache.Del(ctx, key).Err()
}

func (s *SessionStore) DeleteRequestByState(ctx context.Context, state string) error {
	didKey, err := s.getRequestKeyFromState(ctx, state)
	if err != nil {
		return err
	}

	err = s.cache.Del(ctx, fmt.Sprintf(stateKey, state)).Err()
	if err != nil {
		return err
	}

	return s.cache.Del(ctx, didKey).Err()
}

func (s *SessionStore) RefreshSession(ctx context.Context, did, access, refresh, expiry string) error {
	session, err := s.GetSession(ctx, did)
	if err != nil {
		return err
	}
	session.AccessJwt = access
	session.RefreshJwt = refresh
	session.Expiry = expiry
	return s.SaveSession(ctx, *session)
}

func (s *SessionStore) UpdateNonce(ctx context.Context, did, nonce string) error {
	session, err := s.GetSession(ctx, did)
	if err != nil {
		return err
	}
	session.DpopAuthserverNonce = nonce
	return s.SaveSession(ctx, *session)
}

func (s *SessionStore) getRequestKeyFromState(ctx context.Context, state string) (string, error) {
	key := fmt.Sprintf(stateKey, state)
	did, err := s.cache.Get(ctx, key).Result()
	if err != nil {
		return "", err
	}

	didKey := fmt.Sprintf(requestKey, did)
	return didKey, nil
}
