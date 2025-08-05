package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	vault "github.com/openbao/openbao/api/v2"
)

type OpenBaoManager struct {
	client    *vault.Client
	mountPath string
	logger    *slog.Logger
}

type OpenBaoManagerOpt func(*OpenBaoManager)

func WithMountPath(mountPath string) OpenBaoManagerOpt {
	return func(v *OpenBaoManager) {
		v.mountPath = mountPath
	}
}

// NewOpenBaoManager creates a new OpenBao manager that connects to a Bao Proxy
// The proxyAddress should point to the local Bao Proxy (e.g., "http://127.0.0.1:8200")
// The proxy handles all authentication automatically via Auto-Auth
func NewOpenBaoManager(proxyAddress string, logger *slog.Logger, opts ...OpenBaoManagerOpt) (*OpenBaoManager, error) {
	if proxyAddress == "" {
		return nil, fmt.Errorf("proxy address cannot be empty")
	}

	config := vault.DefaultConfig()
	config.Address = proxyAddress

	client, err := vault.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create openbao client: %w", err)
	}

	manager := &OpenBaoManager{
		client:    client,
		mountPath: "spindle", // default KV v2 mount path
		logger:    logger,
	}

	for _, opt := range opts {
		opt(manager)
	}

	if err := manager.testConnection(); err != nil {
		return nil, fmt.Errorf("failed to connect to bao proxy: %w", err)
	}

	logger.Info("successfully connected to bao proxy", "address", proxyAddress)
	return manager, nil
}

// testConnection verifies that we can connect to the proxy
func (v *OpenBaoManager) testConnection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// try token self-lookup as a quick way to verify proxy works
	// and is authenticated
	_, err := v.client.Auth().Token().LookupSelfWithContext(ctx)
	if err != nil {
		return fmt.Errorf("proxy connection test failed: %w", err)
	}

	return nil
}

func (v *OpenBaoManager) AddSecret(ctx context.Context, secret UnlockedSecret) error {
	if err := ValidateKey(secret.Key); err != nil {
		return err
	}

	secretPath := v.buildSecretPath(secret.Repo, secret.Key)
	v.logger.Debug("adding secret", "repo", secret.Repo, "key", secret.Key, "path", secretPath)

	// Check if secret already exists
	existing, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
	if err == nil && existing != nil {
		v.logger.Debug("secret already exists", "path", secretPath)
		return ErrKeyAlreadyPresent
	}

	secretData := map[string]interface{}{
		"value":      secret.Value,
		"repo":       string(secret.Repo),
		"key":        secret.Key,
		"created_at": secret.CreatedAt.Format(time.RFC3339),
		"created_by": secret.CreatedBy.String(),
	}

	v.logger.Debug("writing secret to openbao", "path", secretPath, "mount", v.mountPath)
	resp, err := v.client.KVv2(v.mountPath).Put(ctx, secretPath, secretData)
	if err != nil {
		v.logger.Error("failed to write secret", "path", secretPath, "error", err)
		return fmt.Errorf("failed to store secret in openbao: %w", err)
	}

	v.logger.Debug("secret write response", "version", resp.VersionMetadata.Version, "created_time", resp.VersionMetadata.CreatedTime)

	v.logger.Debug("verifying secret was written", "path", secretPath)
	readBack, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
	if err != nil {
		v.logger.Error("failed to verify secret after write", "path", secretPath, "error", err)
		return fmt.Errorf("secret not found after writing to %s/%s: %w", v.mountPath, secretPath, err)
	}

	if readBack == nil || readBack.Data == nil {
		v.logger.Error("secret verification returned empty data", "path", secretPath)
		return fmt.Errorf("secret verification failed: empty data returned for %s/%s", v.mountPath, secretPath)
	}

	v.logger.Info("secret added and verified successfully", "repo", secret.Repo, "key", secret.Key, "version", readBack.VersionMetadata.Version)
	return nil
}

func (v *OpenBaoManager) RemoveSecret(ctx context.Context, secret Secret[any]) error {
	secretPath := v.buildSecretPath(secret.Repo, secret.Key)

	// check if secret exists
	existing, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
	if err != nil || existing == nil {
		return ErrKeyNotFound
	}

	err = v.client.KVv2(v.mountPath).Delete(ctx, secretPath)
	if err != nil {
		return fmt.Errorf("failed to delete secret from openbao: %w", err)
	}

	v.logger.Debug("secret removed successfully", "repo", secret.Repo, "key", secret.Key)
	return nil
}

func (v *OpenBaoManager) GetSecretsLocked(ctx context.Context, repo DidSlashRepo) ([]LockedSecret, error) {
	repoPath := v.buildRepoPath(repo)

	secretsList, err := v.client.Logical().ListWithContext(ctx, fmt.Sprintf("%s/metadata/%s", v.mountPath, repoPath))
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

		secretPath := fmt.Sprintf("%s/%s", repoPath, key)
		secretData, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
		if err != nil {
			v.logger.Warn("failed to read secret metadata", "path", secretPath, "error", err)
			continue
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

	v.logger.Debug("retrieved locked secrets", "repo", repo, "count", len(secrets))
	return secrets, nil
}

func (v *OpenBaoManager) GetSecretsUnlocked(ctx context.Context, repo DidSlashRepo) ([]UnlockedSecret, error) {
	repoPath := v.buildRepoPath(repo)

	secretsList, err := v.client.Logical().ListWithContext(ctx, fmt.Sprintf("%s/metadata/%s", v.mountPath, repoPath))
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

		secretPath := fmt.Sprintf("%s/%s", repoPath, key)
		secretData, err := v.client.KVv2(v.mountPath).Get(ctx, secretPath)
		if err != nil {
			v.logger.Warn("failed to read secret", "path", secretPath, "error", err)
			continue
		}

		if secretData == nil || secretData.Data == nil {
			continue
		}

		data := secretData.Data

		valueStr, ok := data["value"].(string)
		if !ok {
			v.logger.Warn("secret missing value", "path", secretPath)
			continue
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

	v.logger.Debug("retrieved unlocked secrets", "repo", repo, "count", len(secrets))
	return secrets, nil
}

// buildRepoPath creates a safe path for a repository
func (v *OpenBaoManager) buildRepoPath(repo DidSlashRepo) string {
	// convert DidSlashRepo to a safe path by replacing special characters
	repoPath := strings.ReplaceAll(string(repo), "/", "_")
	repoPath = strings.ReplaceAll(repoPath, ":", "_")
	repoPath = strings.ReplaceAll(repoPath, ".", "_")
	return fmt.Sprintf("repos/%s", repoPath)
}

// buildSecretPath creates a path for a specific secret
func (v *OpenBaoManager) buildSecretPath(repo DidSlashRepo, key string) string {
	return path.Join(v.buildRepoPath(repo), key)
}
