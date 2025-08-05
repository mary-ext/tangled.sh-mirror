package secrets

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/stretchr/testify/assert"
)

// MockOpenBaoManager is a mock implementation of Manager interface for testing
type MockOpenBaoManager struct {
	secrets       map[string]UnlockedSecret // key: repo_key format
	shouldError   bool
	errorToReturn error
}

func NewMockOpenBaoManager() *MockOpenBaoManager {
	return &MockOpenBaoManager{secrets: make(map[string]UnlockedSecret)}
}

func (m *MockOpenBaoManager) SetError(err error) {
	m.shouldError = true
	m.errorToReturn = err
}

func (m *MockOpenBaoManager) ClearError() {
	m.shouldError = false
	m.errorToReturn = nil
}

func (m *MockOpenBaoManager) buildKey(repo DidSlashRepo, key string) string {
	return string(repo) + "_" + key
}

func (m *MockOpenBaoManager) AddSecret(ctx context.Context, secret UnlockedSecret) error {
	if m.shouldError {
		return m.errorToReturn
	}

	key := m.buildKey(secret.Repo, secret.Key)
	if _, exists := m.secrets[key]; exists {
		return ErrKeyAlreadyPresent
	}

	m.secrets[key] = secret
	return nil
}

func (m *MockOpenBaoManager) RemoveSecret(ctx context.Context, secret Secret[any]) error {
	if m.shouldError {
		return m.errorToReturn
	}

	key := m.buildKey(secret.Repo, secret.Key)
	if _, exists := m.secrets[key]; !exists {
		return ErrKeyNotFound
	}

	delete(m.secrets, key)
	return nil
}

func (m *MockOpenBaoManager) GetSecretsLocked(ctx context.Context, repo DidSlashRepo) ([]LockedSecret, error) {
	if m.shouldError {
		return nil, m.errorToReturn
	}

	var result []LockedSecret
	for _, secret := range m.secrets {
		if secret.Repo == repo {
			result = append(result, LockedSecret{
				Key:       secret.Key,
				Repo:      secret.Repo,
				CreatedAt: secret.CreatedAt,
				CreatedBy: secret.CreatedBy,
			})
		}
	}

	return result, nil
}

func (m *MockOpenBaoManager) GetSecretsUnlocked(ctx context.Context, repo DidSlashRepo) ([]UnlockedSecret, error) {
	if m.shouldError {
		return nil, m.errorToReturn
	}

	var result []UnlockedSecret
	for _, secret := range m.secrets {
		if secret.Repo == repo {
			result = append(result, secret)
		}
	}

	return result, nil
}

func createTestSecretForOpenBao(repo, key, value, createdBy string) UnlockedSecret {
	return UnlockedSecret{
		Key:       key,
		Value:     value,
		Repo:      DidSlashRepo(repo),
		CreatedAt: time.Now(),
		CreatedBy: syntax.DID(createdBy),
	}
}

// Test MockOpenBaoManager interface compliance
func TestMockOpenBaoManagerInterface(t *testing.T) {
	var _ Manager = (*MockOpenBaoManager)(nil)
}

func TestOpenBaoManagerInterface(t *testing.T) {
	var _ Manager = (*OpenBaoManager)(nil)
}

func TestNewOpenBaoManager(t *testing.T) {
	tests := []struct {
		name          string
		proxyAddr     string
		opts          []OpenBaoManagerOpt
		expectError   bool
		errorContains string
	}{
		{
			name:          "empty proxy address",
			proxyAddr:     "",
			opts:          nil,
			expectError:   true,
			errorContains: "proxy address cannot be empty",
		},
		{
			name:          "valid proxy address",
			proxyAddr:     "http://localhost:8200",
			opts:          nil,
			expectError:   true, // Will fail because no real proxy is running
			errorContains: "failed to connect to bao proxy",
		},
		{
			name:          "with mount path option",
			proxyAddr:     "http://localhost:8200",
			opts:          []OpenBaoManagerOpt{WithMountPath("custom-mount")},
			expectError:   true, // Will fail because no real proxy is running
			errorContains: "failed to connect to bao proxy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			manager, err := NewOpenBaoManager(tt.proxyAddr, logger, tt.opts...)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, manager)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, manager)
			}
		})
	}
}

func TestOpenBaoManager_PathBuilding(t *testing.T) {
	manager := &OpenBaoManager{mountPath: "secret"}

	tests := []struct {
		name     string
		repo     DidSlashRepo
		key      string
		expected string
	}{
		{
			name:     "simple repo path",
			repo:     DidSlashRepo("did:plc:foo/repo"),
			key:      "api_key",
			expected: "repos/did_plc_foo_repo/api_key",
		},
		{
			name:     "complex repo path with dots",
			repo:     DidSlashRepo("did:web:example.com/my-repo"),
			key:      "secret_key",
			expected: "repos/did_web_example_com_my-repo/secret_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.buildSecretPath(tt.repo, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOpenBaoManager_buildRepoPath(t *testing.T) {
	manager := &OpenBaoManager{mountPath: "test"}

	tests := []struct {
		name     string
		repo     DidSlashRepo
		expected string
	}{
		{
			name:     "simple repo",
			repo:     "did:plc:test/myrepo",
			expected: "repos/did_plc_test_myrepo",
		},
		{
			name:     "repo with dots",
			repo:     "did:plc:example.com/my.repo",
			expected: "repos/did_plc_example_com_my_repo",
		},
		{
			name:     "complex repo",
			repo:     "did:web:example.com:8080/path/to/repo",
			expected: "repos/did_web_example_com_8080_path_to_repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.buildRepoPath(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWithMountPath(t *testing.T) {
	manager := &OpenBaoManager{mountPath: "default"}

	opt := WithMountPath("custom-mount")
	opt(manager)

	assert.Equal(t, "custom-mount", manager.mountPath)
}

func TestMockOpenBaoManager_AddSecret(t *testing.T) {
	tests := []struct {
		name        string
		secrets     []UnlockedSecret
		expectError bool
	}{
		{
			name: "add single secret",
			secrets: []UnlockedSecret{
				createTestSecretForOpenBao("did:plc:test/repo1", "API_KEY", "secret123", "did:plc:creator"),
			},
			expectError: false,
		},
		{
			name: "add multiple secrets",
			secrets: []UnlockedSecret{
				createTestSecretForOpenBao("did:plc:test/repo1", "API_KEY", "secret123", "did:plc:creator"),
				createTestSecretForOpenBao("did:plc:test/repo1", "DB_PASSWORD", "dbpass456", "did:plc:creator"),
			},
			expectError: false,
		},
		{
			name: "add duplicate secret",
			secrets: []UnlockedSecret{
				createTestSecretForOpenBao("did:plc:test/repo1", "API_KEY", "secret123", "did:plc:creator"),
				createTestSecretForOpenBao("did:plc:test/repo1", "API_KEY", "newsecret", "did:plc:creator"),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockOpenBaoManager()
			ctx := context.Background()
			var err error

			for i, secret := range tt.secrets {
				err = mock.AddSecret(ctx, secret)
				if tt.expectError && i == 1 { // Second secret should fail for duplicate test
					assert.Equal(t, ErrKeyAlreadyPresent, err)
					return
				}
				if !tt.expectError {
					assert.NoError(t, err)
				}
			}

			if !tt.expectError {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMockOpenBaoManager_RemoveSecret(t *testing.T) {
	tests := []struct {
		name         string
		setupSecrets []UnlockedSecret
		removeSecret Secret[any]
		expectError  bool
	}{
		{
			name: "remove existing secret",
			setupSecrets: []UnlockedSecret{
				createTestSecretForOpenBao("did:plc:test/repo1", "API_KEY", "secret123", "did:plc:creator"),
			},
			removeSecret: Secret[any]{
				Key:  "API_KEY",
				Repo: DidSlashRepo("did:plc:test/repo1"),
			},
			expectError: false,
		},
		{
			name:         "remove non-existent secret",
			setupSecrets: []UnlockedSecret{},
			removeSecret: Secret[any]{
				Key:  "API_KEY",
				Repo: DidSlashRepo("did:plc:test/repo1"),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockOpenBaoManager()
			ctx := context.Background()

			// Setup secrets
			for _, secret := range tt.setupSecrets {
				err := mock.AddSecret(ctx, secret)
				assert.NoError(t, err)
			}

			// Remove secret
			err := mock.RemoveSecret(ctx, tt.removeSecret)

			if tt.expectError {
				assert.Equal(t, ErrKeyNotFound, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMockOpenBaoManager_GetSecretsLocked(t *testing.T) {
	tests := []struct {
		name          string
		setupSecrets  []UnlockedSecret
		queryRepo     DidSlashRepo
		expectedCount int
		expectedKeys  []string
		expectError   bool
	}{
		{
			name: "get secrets from repo with secrets",
			setupSecrets: []UnlockedSecret{
				createTestSecretForOpenBao("did:plc:test/repo1", "API_KEY", "secret123", "did:plc:creator"),
				createTestSecretForOpenBao("did:plc:test/repo1", "DB_PASSWORD", "dbpass456", "did:plc:creator"),
				createTestSecretForOpenBao("did:plc:test/repo2", "OTHER_KEY", "other789", "did:plc:creator"),
			},
			queryRepo:     DidSlashRepo("did:plc:test/repo1"),
			expectedCount: 2,
			expectedKeys:  []string{"API_KEY", "DB_PASSWORD"},
			expectError:   false,
		},
		{
			name:          "get secrets from empty repo",
			setupSecrets:  []UnlockedSecret{},
			queryRepo:     DidSlashRepo("did:plc:test/empty"),
			expectedCount: 0,
			expectedKeys:  []string{},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockOpenBaoManager()
			ctx := context.Background()

			// Setup
			for _, secret := range tt.setupSecrets {
				err := mock.AddSecret(ctx, secret)
				assert.NoError(t, err)
			}

			// Test
			secrets, err := mock.GetSecretsLocked(ctx, tt.queryRepo)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, secrets, tt.expectedCount)

				// Check keys
				actualKeys := make([]string, len(secrets))
				for i, secret := range secrets {
					actualKeys[i] = secret.Key
				}

				for _, expectedKey := range tt.expectedKeys {
					assert.Contains(t, actualKeys, expectedKey)
				}
			}
		})
	}
}

func TestMockOpenBaoManager_GetSecretsUnlocked(t *testing.T) {
	tests := []struct {
		name            string
		setupSecrets    []UnlockedSecret
		queryRepo       DidSlashRepo
		expectedCount   int
		expectedSecrets map[string]string // key -> value
		expectError     bool
	}{
		{
			name: "get unlocked secrets from repo",
			setupSecrets: []UnlockedSecret{
				createTestSecretForOpenBao("did:plc:test/repo1", "API_KEY", "secret123", "did:plc:creator"),
				createTestSecretForOpenBao("did:plc:test/repo1", "DB_PASSWORD", "dbpass456", "did:plc:creator"),
				createTestSecretForOpenBao("did:plc:test/repo2", "OTHER_KEY", "other789", "did:plc:creator"),
			},
			queryRepo:     DidSlashRepo("did:plc:test/repo1"),
			expectedCount: 2,
			expectedSecrets: map[string]string{
				"API_KEY":     "secret123",
				"DB_PASSWORD": "dbpass456",
			},
			expectError: false,
		},
		{
			name:            "get secrets from empty repo",
			setupSecrets:    []UnlockedSecret{},
			queryRepo:       DidSlashRepo("did:plc:test/empty"),
			expectedCount:   0,
			expectedSecrets: map[string]string{},
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockOpenBaoManager()
			ctx := context.Background()

			// Setup
			for _, secret := range tt.setupSecrets {
				err := mock.AddSecret(ctx, secret)
				assert.NoError(t, err)
			}

			// Test
			secrets, err := mock.GetSecretsUnlocked(ctx, tt.queryRepo)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, secrets, tt.expectedCount)

				// Check key-value pairs
				actualSecrets := make(map[string]string)
				for _, secret := range secrets {
					actualSecrets[secret.Key] = secret.Value
				}

				for expectedKey, expectedValue := range tt.expectedSecrets {
					actualValue, exists := actualSecrets[expectedKey]
					assert.True(t, exists, "Expected key %s not found", expectedKey)
					assert.Equal(t, expectedValue, actualValue)
				}
			}
		})
	}
}

func TestMockOpenBaoManager_ErrorHandling(t *testing.T) {
	mock := NewMockOpenBaoManager()
	ctx := context.Background()
	testError := assert.AnError

	// Test error injection
	mock.SetError(testError)

	secret := createTestSecretForOpenBao("did:plc:test/repo1", "API_KEY", "secret123", "did:plc:creator")

	// All operations should return the injected error
	err := mock.AddSecret(ctx, secret)
	assert.Equal(t, testError, err)

	_, err = mock.GetSecretsLocked(ctx, "did:plc:test/repo1")
	assert.Equal(t, testError, err)

	_, err = mock.GetSecretsUnlocked(ctx, "did:plc:test/repo1")
	assert.Equal(t, testError, err)

	err = mock.RemoveSecret(ctx, Secret[any]{Key: "API_KEY", Repo: "did:plc:test/repo1"})
	assert.Equal(t, testError, err)

	// Clear error and test normal operation
	mock.ClearError()
	err = mock.AddSecret(ctx, secret)
	assert.NoError(t, err)
}

func TestMockOpenBaoManager_Integration(t *testing.T) {
	tests := []struct {
		name     string
		scenario func(t *testing.T, mock *MockOpenBaoManager)
	}{
		{
			name: "complete workflow",
			scenario: func(t *testing.T, mock *MockOpenBaoManager) {
				ctx := context.Background()
				repo := DidSlashRepo("did:plc:test/integration")

				// Start with empty repo
				secrets, err := mock.GetSecretsLocked(ctx, repo)
				assert.NoError(t, err)
				assert.Empty(t, secrets)

				// Add some secrets
				secret1 := createTestSecretForOpenBao(string(repo), "API_KEY", "secret123", "did:plc:creator")
				secret2 := createTestSecretForOpenBao(string(repo), "DB_PASSWORD", "dbpass456", "did:plc:creator")

				err = mock.AddSecret(ctx, secret1)
				assert.NoError(t, err)

				err = mock.AddSecret(ctx, secret2)
				assert.NoError(t, err)

				// Verify secrets exist
				secrets, err = mock.GetSecretsLocked(ctx, repo)
				assert.NoError(t, err)
				assert.Len(t, secrets, 2)

				unlockedSecrets, err := mock.GetSecretsUnlocked(ctx, repo)
				assert.NoError(t, err)
				assert.Len(t, unlockedSecrets, 2)

				// Remove one secret
				err = mock.RemoveSecret(ctx, Secret[any]{Key: "API_KEY", Repo: repo})
				assert.NoError(t, err)

				// Verify only one secret remains
				secrets, err = mock.GetSecretsLocked(ctx, repo)
				assert.NoError(t, err)
				assert.Len(t, secrets, 1)
				assert.Equal(t, "DB_PASSWORD", secrets[0].Key)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockOpenBaoManager()
			tt.scenario(t, mock)
		})
	}
}

func TestOpenBaoManager_ProxyConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		proxyAddr   string
		description string
	}{
		{
			name:        "default_localhost",
			proxyAddr:   "http://127.0.0.1:8200",
			description: "Should connect to default localhost proxy",
		},
		{
			name:        "custom_host",
			proxyAddr:   "http://bao-proxy:8200",
			description: "Should connect to custom proxy host",
		},
		{
			name:        "https_proxy",
			proxyAddr:   "https://127.0.0.1:8200",
			description: "Should connect to HTTPS proxy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Testing scenario:", tt.description)
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

			// All these will fail because no real proxy is running
			// but we can test that the configuration is properly accepted
			manager, err := NewOpenBaoManager(tt.proxyAddr, logger)
			assert.Error(t, err) // Expected because no real proxy
			assert.Nil(t, manager)
			assert.Contains(t, err.Error(), "failed to connect to bao proxy")
		})
	}
}
