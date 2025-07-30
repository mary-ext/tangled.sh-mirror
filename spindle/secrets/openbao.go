package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	vault "github.com/openbao/openbao/api/v2"
)

type OpenBaoManager struct {
	client    *vault.Client
	mountPath string
	roleID    string
	secretID  string
	stopCh    chan struct{}
	tokenMu   sync.RWMutex
	logger    *slog.Logger
}

type OpenBaoManagerOpt func(*OpenBaoManager)

func WithMountPath(mountPath string) OpenBaoManagerOpt {
	return func(v *OpenBaoManager) {
		v.mountPath = mountPath
	}
}

func NewOpenBaoManager(address, roleID, secretID string, logger *slog.Logger, opts ...OpenBaoManagerOpt) (*OpenBaoManager, error) {
	if address == "" {
		return nil, fmt.Errorf("address cannot be empty")
	}
	if roleID == "" {
		return nil, fmt.Errorf("role_id cannot be empty")
	}
	if secretID == "" {
		return nil, fmt.Errorf("secret_id cannot be empty")
	}

	config := vault.DefaultConfig()
	config.Address = address

	client, err := vault.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create openbao client: %w", err)
	}

	// Authenticate using AppRole
	err = authenticateAppRole(client, roleID, secretID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate with AppRole: %w", err)
	}

	manager := &OpenBaoManager{
		client:    client,
		mountPath: "spindle", // default KV v2 mount path
		roleID:    roleID,
		secretID:  secretID,
		stopCh:    make(chan struct{}),
		logger:    logger,
	}

	for _, opt := range opts {
		opt(manager)
	}

	go manager.tokenRenewalLoop()

	return manager, nil
}

// authenticateAppRole authenticates the client using AppRole method
func authenticateAppRole(client *vault.Client, roleID, secretID string) error {
	authData := map[string]interface{}{
		"role_id":   roleID,
		"secret_id": secretID,
	}

	resp, err := client.Logical().Write("auth/approle/login", authData)
	if err != nil {
		return fmt.Errorf("failed to login with AppRole: %w", err)
	}

	if resp == nil || resp.Auth == nil {
		return fmt.Errorf("no auth info returned from AppRole login")
	}

	client.SetToken(resp.Auth.ClientToken)
	return nil
}

// stop stops the token renewal goroutine
func (v *OpenBaoManager) Stop() {
	close(v.stopCh)
}

// tokenRenewalLoop runs in a background goroutine to automatically renew or re-authenticate tokens
func (v *OpenBaoManager) tokenRenewalLoop() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-v.stopCh:
			return
		case <-ticker.C:
			ctx := context.Background()
			if err := v.ensureValidToken(ctx); err != nil {
				v.logger.Error("openbao token renewal failed", "error", err)
			}
		}
	}
}

// ensureValidToken checks if the current token is valid and renews or re-authenticates if needed
func (v *OpenBaoManager) ensureValidToken(ctx context.Context) error {
	v.tokenMu.Lock()
	defer v.tokenMu.Unlock()

	// check current token info
	tokenInfo, err := v.client.Auth().Token().LookupSelf()
	if err != nil {
		// token is invalid, need to re-authenticate
		v.logger.Warn("token lookup failed, re-authenticating", "error", err)
		return v.reAuthenticate()
	}

	if tokenInfo == nil || tokenInfo.Data == nil {
		return v.reAuthenticate()
	}

	// check TTL
	ttlRaw, ok := tokenInfo.Data["ttl"]
	if !ok {
		return v.reAuthenticate()
	}

	var ttl int64
	switch t := ttlRaw.(type) {
	case int64:
		ttl = t
	case float64:
		ttl = int64(t)
	case int:
		ttl = int64(t)
	default:
		return v.reAuthenticate()
	}

	// if TTL is less than 5 minutes, try to renew
	if ttl < 300 {
		v.logger.Info("token ttl low, attempting renewal", "ttl_seconds", ttl)

		renewResp, err := v.client.Auth().Token().RenewSelf(3600) // 1h
		if err != nil {
			v.logger.Warn("token renewal failed, re-authenticating", "error", err)
			return v.reAuthenticate()
		}

		if renewResp == nil || renewResp.Auth == nil {
			v.logger.Warn("token renewal returned no auth info, re-authenticating")
			return v.reAuthenticate()
		}

		v.logger.Info("token renewed successfully", "new_ttl_seconds", renewResp.Auth.LeaseDuration)
	}

	return nil
}

// reAuthenticate performs a fresh authentication using AppRole
func (v *OpenBaoManager) reAuthenticate() error {
	v.logger.Info("re-authenticating with approle")

	err := authenticateAppRole(v.client, v.roleID, v.secretID)
	if err != nil {
		return fmt.Errorf("re-authentication failed: %w", err)
	}

	v.logger.Info("re-authentication successful")
	return nil
}

func (v *OpenBaoManager) AddSecret(ctx context.Context, secret UnlockedSecret) error {
	v.tokenMu.RLock()
	defer v.tokenMu.RUnlock()
	if err := ValidateKey(secret.Key); err != nil {
		return err
	}

	secretPath := v.buildSecretPath(secret.Repo, secret.Key)

	fmt.Println(v.mountPath, secretPath)

	existing, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
	if err == nil && existing != nil {
		return ErrKeyAlreadyPresent
	}

	secretData := map[string]interface{}{
		"value":      secret.Value,
		"repo":       string(secret.Repo),
		"key":        secret.Key,
		"created_at": secret.CreatedAt.Format(time.RFC3339),
		"created_by": secret.CreatedBy.String(),
	}

	_, err = v.client.KVv2(v.mountPath).Put(ctx, secretPath, secretData)
	if err != nil {
		return fmt.Errorf("failed to store secret in openbao: %w", err)
	}

	return nil
}

func (v *OpenBaoManager) RemoveSecret(ctx context.Context, secret Secret[any]) error {
	v.tokenMu.RLock()
	defer v.tokenMu.RUnlock()
	secretPath := v.buildSecretPath(secret.Repo, secret.Key)

	existing, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
	if err != nil || existing == nil {
		return ErrKeyNotFound
	}

	err = v.client.KVv2(v.mountPath).Delete(ctx, secretPath)
	if err != nil {
		return fmt.Errorf("failed to delete secret from openbao: %w", err)
	}

	return nil
}

func (v *OpenBaoManager) GetSecretsLocked(ctx context.Context, repo DidSlashRepo) ([]LockedSecret, error) {
	v.tokenMu.RLock()
	defer v.tokenMu.RUnlock()
	repoPath := v.buildRepoPath(repo)

	secretsList, err := v.client.Logical().List(fmt.Sprintf("%s/metadata/%s", v.mountPath, repoPath))
	if err != nil {
		if strings.Contains(err.Error(), "no secret found") || strings.Contains(err.Error(), "no handler for route") {
			return []LockedSecret{}, nil
		}
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	if secretsList == nil || secretsList.Data == nil {
		return []LockedSecret{}, nil
	}

	keys, ok := secretsList.Data["keys"].([]interface{})
	if !ok {
		return []LockedSecret{}, nil
	}

	var secrets []LockedSecret

	for _, keyInterface := range keys {
		key, ok := keyInterface.(string)
		if !ok {
			continue
		}

		secretPath := path.Join(repoPath, key)
		secretData, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
		if err != nil {
			continue // Skip secrets we can't read
		}

		if secretData == nil || secretData.Data == nil {
			continue
		}

		data := secretData.Data

		createdAtStr, ok := data["created_at"].(string)
		if !ok {
			createdAtStr = time.Now().Format(time.RFC3339)
		}

		createdAt, err := time.Parse(time.RFC3339, createdAtStr)
		if err != nil {
			createdAt = time.Now()
		}

		createdByStr, ok := data["created_by"].(string)
		if !ok {
			createdByStr = ""
		}

		keyStr, ok := data["key"].(string)
		if !ok {
			keyStr = key
		}

		secret := LockedSecret{
			Key:       keyStr,
			Repo:      repo,
			CreatedAt: createdAt,
			CreatedBy: syntax.DID(createdByStr),
		}

		secrets = append(secrets, secret)
	}

	return secrets, nil
}

func (v *OpenBaoManager) GetSecretsUnlocked(ctx context.Context, repo DidSlashRepo) ([]UnlockedSecret, error) {
	v.tokenMu.RLock()
	defer v.tokenMu.RUnlock()
	repoPath := v.buildRepoPath(repo)

	secretsList, err := v.client.Logical().List(fmt.Sprintf("%s/metadata/%s", v.mountPath, repoPath))
	if err != nil {
		if strings.Contains(err.Error(), "no secret found") || strings.Contains(err.Error(), "no handler for route") {
			return []UnlockedSecret{}, nil
		}
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	if secretsList == nil || secretsList.Data == nil {
		return []UnlockedSecret{}, nil
	}

	keys, ok := secretsList.Data["keys"].([]interface{})
	if !ok {
		return []UnlockedSecret{}, nil
	}

	var secrets []UnlockedSecret

	for _, keyInterface := range keys {
		key, ok := keyInterface.(string)
		if !ok {
			continue
		}

		secretPath := path.Join(repoPath, key)
		secretData, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
		if err != nil {
			continue
		}

		if secretData == nil || secretData.Data == nil {
			continue
		}

		data := secretData.Data

		valueStr, ok := data["value"].(string)
		if !ok {
			continue // skip secrets without values
		}

		createdAtStr, ok := data["created_at"].(string)
		if !ok {
			createdAtStr = time.Now().Format(time.RFC3339)
		}

		createdAt, err := time.Parse(time.RFC3339, createdAtStr)
		if err != nil {
			createdAt = time.Now()
		}

		createdByStr, ok := data["created_by"].(string)
		if !ok {
			createdByStr = ""
		}

		keyStr, ok := data["key"].(string)
		if !ok {
			keyStr = key
		}

		secret := UnlockedSecret{
			Key:       keyStr,
			Value:     valueStr,
			Repo:      repo,
			CreatedAt: createdAt,
			CreatedBy: syntax.DID(createdByStr),
		}

		secrets = append(secrets, secret)
	}

	return secrets, nil
}

// buildRepoPath creates an OpenBao path for a repository
func (v *OpenBaoManager) buildRepoPath(repo DidSlashRepo) string {
	// convert DidSlashRepo to a safe path by replacing special characters
	repoPath := strings.ReplaceAll(string(repo), "/", "_")
	repoPath = strings.ReplaceAll(repoPath, ":", "_")
	repoPath = strings.ReplaceAll(repoPath, ".", "_")
	return fmt.Sprintf("repos/%s", repoPath)
}

// buildSecretPath creates an OpenBao path for a specific secret
func (v *OpenBaoManager) buildSecretPath(repo DidSlashRepo, key string) string {
	return path.Join(v.buildRepoPath(repo), key)
}
