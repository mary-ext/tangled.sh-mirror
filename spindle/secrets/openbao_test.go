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
		Repo:      DidSlashRepo(repo),
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

func TestOpenBaoManager_BuildRepoPath(t *testing.T) {
	manager := &OpenBaoManager{mountPath: "secret"}

	tests := []struct {
		name     string
		repo     DidSlashRepo
		expected string
	}{
		{
			name:     "basic repo",
			repo:     DidSlashRepo("did:plc:foo/repo"),
			expected: "repos/did_plc_foo_repo",
		},
		{
			name:     "web DID with domain",
			repo:     DidSlashRepo("did:web:example.com/my-repo"),
			expected: "repos/did_web_example_com_my-repo",
		},
		{
			name:     "complex path",
			repo:     DidSlashRepo("did:web:sub.example.com:8080/complex-repo-name"),
			expected: "repos/did_web_sub_example_com_8080_complex-repo-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.buildRepoPath(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}
