package secrets

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	vault "github.com/openbao/openbao/api/v2"
)

type OpenBaoManager struct {
	client    *vault.Client
	mountPath string
}

type OpenBaoManagerOpt func(*OpenBaoManager)

func WithMountPath(mountPath string) OpenBaoManagerOpt {
	return func(v *OpenBaoManager) {
		v.mountPath = mountPath
	}
}

func NewOpenBaoManager(address, token string, opts ...OpenBaoManagerOpt) (*OpenBaoManager, error) {
	config := vault.DefaultConfig()
	config.Address = address

	client, err := vault.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create openbao client: %w", err)
	}

	client.SetToken(token)

	manager := &OpenBaoManager{
		client:    client,
		mountPath: "secret", // default KV v2 mount path
	}

	for _, opt := range opts {
		opt(manager)
	}

	return manager, nil
}

func (v *OpenBaoManager) AddSecret(ctx context.Context, secret UnlockedSecret) error {
	if err := ValidateKey(secret.Key); err != nil {
		return err
	}

	secretPath := v.buildSecretPath(secret.Repo, secret.Key)

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
