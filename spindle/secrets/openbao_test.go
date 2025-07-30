package secrets

import (
	"log/slog"
	"os"
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
		name          string
		address       string
		roleID        string
		secretID      string
		opts          []OpenBaoManagerOpt
		expectError   bool
		errorContains string
	}{
		{
			name:          "empty address",
			address:       "",
			roleID:        "test-role-id",
			secretID:      "test-secret-id",
			opts:          nil,
			expectError:   true,
			errorContains: "address cannot be empty",
		},
		{
			name:          "empty role_id",
			address:       "http://localhost:8200",
			roleID:        "",
			secretID:      "test-secret-id",
			opts:          nil,
			expectError:   true,
			errorContains: "role_id cannot be empty",
		},
		{
			name:          "empty secret_id",
			address:       "http://localhost:8200",
			roleID:        "test-role-id",
			secretID:      "",
			opts:          nil,
			expectError:   true,
			errorContains: "secret_id cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			manager, err := NewOpenBaoManager(tt.address, tt.roleID, tt.secretID, logger, tt.opts...)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, manager)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				// For valid configurations, we expect an error during authentication
				// since we're not connecting to a real OpenBao server
				assert.Error(t, err)
				assert.Nil(t, manager)
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

func TestOpenBaoManager_Stop(t *testing.T) {
	// Create a manager with minimal setup
	manager := &OpenBaoManager{
		mountPath: "test",
		stopCh:    make(chan struct{}),
	}

	// Verify the manager implements Stopper interface
	var stopper Stopper = manager
	assert.NotNil(t, stopper)

	// Call Stop and verify it doesn't panic
	assert.NotPanics(t, func() {
		manager.Stop()
	})

	// Verify the channel was closed
	select {
	case <-manager.stopCh:
		// Channel was closed as expected
	default:
		t.Error("Expected stop channel to be closed after Stop()")
	}
}

func TestOpenBaoManager_StopperInterface(t *testing.T) {
	manager := &OpenBaoManager{}

	// Verify that OpenBaoManager implements the Stopper interface
	_, ok := interface{}(manager).(Stopper)
	assert.True(t, ok, "OpenBaoManager should implement Stopper interface")
}
