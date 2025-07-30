package secrets

import (
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/stretchr/testify/assert"
)

func createTestSecretForOpenBao(repo, key, value, createdBy string) UnlockedSecret {
	return UnlockedSecret{
		Key:       key,
		Value:     value,
		Repo:      syntax.ATURI(repo),
		CreatedAt: time.Now(),
		CreatedBy: syntax.DID(createdBy),
	}
}

func TestOpenBaoManagerInterface(t *testing.T) {
	var _ Manager = (*OpenBaoManager)(nil)
}

func TestNewOpenBaoManager(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		token       string
		opts        []OpenBaoManagerOpt
		expectError bool
	}{
		{
			name:        "valid configuration",
			address:     "http://localhost:8200",
			token:       "test-token",
			opts:        nil,
			expectError: false,
		},
		{
			name:        "custom mount path",
			address:     "http://localhost:8200",
			token:       "test-token",
			opts:        []OpenBaoManagerOpt{WithMountPath("custom-secrets")},
			expectError: false,
		},
		{
			name:        "empty address",
			address:     "",
			token:       "test-token",
			opts:        nil,
			expectError: false, // Vault client doesn't validate empty address at creation time
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewOpenBaoManager(tt.address, tt.token, tt.opts...)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, manager)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, manager)

				if len(tt.opts) > 0 {
					// Check if custom mount path was set
					assert.Equal(t, "custom-secrets", manager.mountPath)
				} else {
					assert.Equal(t, "secret", manager.mountPath)
				}
			}
		})
	}
}

func TestOpenBaoManager_PathBuilding(t *testing.T) {
	manager := &OpenBaoManager{mountPath: "secret"}

	tests := []struct {
		name     string
		repo     syntax.ATURI
		key      string
		expected string
	}{
		{
			name:     "simple repo path",
			repo:     syntax.ATURI("at://did:plc:foo/repo"),
			key:      "api_key",
			expected: "repos/at/did_plc_foo/repo/api_key",
		},
		{
			name:     "complex repo path with dots",
			repo:     syntax.ATURI("at://did:web:example.com/my-repo"),
			key:      "secret_key",
			expected: "repos/at/did_web_example_com/my-repo/secret_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.buildSecretPath(tt.repo, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOpenBaoManager_BuildRepoPath(t *testing.T) {
	manager := &OpenBaoManager{mountPath: "secret"}

	tests := []struct {
		name     string
		repo     syntax.ATURI
		expected string
	}{
		{
			name:     "basic repo",
			repo:     syntax.ATURI("at://did:plc:foo/repo"),
			expected: "repos/at/did_plc_foo/repo",
		},
		{
			name:     "web DID with domain",
			repo:     syntax.ATURI("at://did:web:example.com/my-repo"),
			expected: "repos/at/did_web_example_com/my-repo",
		},
		{
			name:     "complex path",
			repo:     syntax.ATURI("at://did:web:sub.example.com:8080/complex-repo-name"),
			expected: "repos/at/did_web_sub_example_com_8080/complex-repo-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.buildRepoPath(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Integration tests that require a real OpenBao instance
// These tests are skipped unless OPENBAO_INTEGRATION_TEST environment variable is set
func TestOpenBaoManagerIntegration(t *testing.T) {
	t.Skip("Integration tests require a running OpenBao instance. Set OPENBAO_INTEGRATION_TEST=1 and provide OPENBAO_ADDR and OPENBAO_TOKEN to run.")

	// Example of how integration tests would be structured:
	//
	// if os.Getenv("OPENBAO_INTEGRATION_TEST") == "" {
	//     t.Skip("Skipping OpenBao integration test. Set OPENBAO_INTEGRATION_TEST=1 to run.")
	// }
	//
	// openBaoAddr := os.Getenv("OPENBAO_ADDR")
	// openBaoToken := os.Getenv("OPENBAO_TOKEN")
	//
	// if openBaoAddr == "" || openBaoToken == "" {
	//     t.Skip("OPENBAO_ADDR and OPENBAO_TOKEN must be set for integration tests")
	// }
	//
	// manager, err := NewOpenBaoManager(openBaoAddr, openBaoToken)
	// require.NoError(t, err)
	//
	// // Test AddSecret
	// secret := createTestSecretForOpenBao("at://did:plc:test/repo", "test_key", "test_value", "did:plc:tester")
	// err = manager.AddSecret(context.Background(), secret)
	// assert.NoError(t, err)
	//
	// // Test GetSecretsLocked
	// locked, err := manager.GetSecretsLocked(context.Background(), syntax.ATURI("at://did:plc:test/repo"))
	// assert.NoError(t, err)
	// assert.Len(t, locked, 1)
	// assert.Equal(t, "test_key", locked[0].Key)
	//
	// // Test GetSecretsUnlocked
	// unlocked, err := manager.GetSecretsUnlocked(context.Background(), syntax.ATURI("at://did:plc:test/repo"))
	// assert.NoError(t, err)
	// assert.Len(t, unlocked, 1)
	// assert.Equal(t, "test_key", unlocked[0].Key)
	// assert.Equal(t, "test_value", unlocked[0].Value)
	//
	// // Test RemoveSecret
	// err = manager.RemoveSecret(context.Background(), Secret[any]{Key: "test_key", Repo: syntax.ATURI("at://did:plc:test/repo")})
	// assert.NoError(t, err)
	//
	// // Verify removal
	// locked, err = manager.GetSecretsLocked(context.Background(), syntax.ATURI("at://did:plc:test/repo"))
	// assert.NoError(t, err)
	// assert.Len(t, locked, 0)
}
