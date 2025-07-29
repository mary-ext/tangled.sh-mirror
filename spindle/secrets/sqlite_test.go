package secrets

import (
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

func createInMemoryDB(t *testing.T) *SqliteManager {
	t.Helper()
	manager, err := NewSQLiteManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to create in-memory manager: %v", err)
	}
	return manager
}

func createTestSecret(repo, key, value, createdBy string) UnlockedSecret {
	return UnlockedSecret{
		Key:       key,
		Value:     value,
		Repo:      DidSlashRepo(repo),
		CreatedAt: time.Now(),
		CreatedBy: syntax.DID(createdBy),
	}
}

// ensure that interface is satisfied
func TestManagerInterface(t *testing.T) {
	var _ Manager = (*SqliteManager)(nil)
}

func TestNewSQLiteManager(t *testing.T) {
	tests := []struct {
		name        string
		dbPath      string
		opts        []SqliteManagerOpt
		expectError bool
		expectTable string
	}{
		{
			name:        "default table name",
			dbPath:      ":memory:",
			opts:        nil,
			expectError: false,
			expectTable: "secrets",
		},
		{
			name:        "custom table name",
			dbPath:      ":memory:",
			opts:        []SqliteManagerOpt{WithTableName("custom_secrets")},
			expectError: false,
			expectTable: "custom_secrets",
		},
		{
			name:        "invalid database path",
			dbPath:      "/invalid/path/to/database.db",
			opts:        nil,
			expectError: true,
			expectTable: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewSQLiteManager(tt.dbPath, tt.opts...)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			defer manager.db.Close()

			if manager.tableName != tt.expectTable {
				t.Errorf("Expected table name %s, got %s", tt.expectTable, manager.tableName)
			}
		})
	}
}

func TestSqliteManager_AddSecret(t *testing.T) {
	tests := []struct {
		name        string
		secrets     []UnlockedSecret
		expectError []error
	}{
		{
			name: "add single secret",
			secrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "api_key", "secret_value_123", "did:plc:example123"),
			},
			expectError: []error{nil},
		},
		{
			name: "add multiple unique secrets",
			secrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "api_key", "secret_value_123", "did:plc:example123"),
				createTestSecret("did:plc:foo/repo", "db_password", "password_456", "did:plc:example123"),
				createTestSecret("other.com/repo", "api_key", "other_secret", "did:plc:other"),
			},
			expectError: []error{nil, nil, nil},
		},
		{
			name: "add duplicate secret",
			secrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "api_key", "secret_value_123", "did:plc:example123"),
				createTestSecret("did:plc:foo/repo", "api_key", "different_value", "did:plc:example123"),
			},
			expectError: []error{nil, ErrKeyAlreadyPresent},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := createInMemoryDB(t)
			defer manager.db.Close()

			for i, secret := range tt.secrets {
				err := manager.AddSecret(secret)
				if err != tt.expectError[i] {
					t.Errorf("Secret %d: expected error %v, got %v", i, tt.expectError[i], err)
				}
			}
		})
	}
}

func TestSqliteManager_RemoveSecret(t *testing.T) {
	tests := []struct {
		name         string
		setupSecrets []UnlockedSecret
		removeSecret Secret[any]
		expectError  error
	}{
		{
			name: "remove existing secret",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "api_key", "secret_value_123", "did:plc:example123"),
			},
			removeSecret: Secret[any]{
				Key:  "api_key",
				Repo: DidSlashRepo("did:plc:foo/repo"),
			},
			expectError: nil,
		},
		{
			name: "remove non-existent secret",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "api_key", "secret_value_123", "did:plc:example123"),
			},
			removeSecret: Secret[any]{
				Key:  "non_existent_key",
				Repo: DidSlashRepo("did:plc:foo/repo"),
			},
			expectError: ErrKeyNotFound,
		},
		{
			name:         "remove from empty database",
			setupSecrets: []UnlockedSecret{},
			removeSecret: Secret[any]{
				Key:  "any_key",
				Repo: DidSlashRepo("did:plc:foo/repo"),
			},
			expectError: ErrKeyNotFound,
		},
		{
			name: "remove secret from wrong repo",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "api_key", "secret_value_123", "did:plc:example123"),
			},
			removeSecret: Secret[any]{
				Key:  "api_key",
				Repo: DidSlashRepo("other.com/repo"),
			},
			expectError: ErrKeyNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := createInMemoryDB(t)
			defer manager.db.Close()

			// Setup secrets
			for _, secret := range tt.setupSecrets {
				if err := manager.AddSecret(secret); err != nil {
					t.Fatalf("Failed to setup secret: %v", err)
				}
			}

			// Test removal
			err := manager.RemoveSecret(tt.removeSecret)
			if err != tt.expectError {
				t.Errorf("Expected error %v, got %v", tt.expectError, err)
			}
		})
	}
}

func TestSqliteManager_GetSecretsLocked(t *testing.T) {
	tests := []struct {
		name          string
		setupSecrets  []UnlockedSecret
		queryRepo     DidSlashRepo
		expectedCount int
		expectedKeys  []string
		expectError   bool
	}{
		{
			name: "get secrets for repo with multiple secrets",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "key1", "value1", "did:plc:user1"),
				createTestSecret("did:plc:foo/repo", "key2", "value2", "did:plc:user2"),
				createTestSecret("other.com/repo", "key3", "value3", "did:plc:user3"),
			},
			queryRepo:     DidSlashRepo("did:plc:foo/repo"),
			expectedCount: 2,
			expectedKeys:  []string{"key1", "key2"},
			expectError:   false,
		},
		{
			name: "get secrets for repo with single secret",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "single_key", "single_value", "did:plc:user1"),
				createTestSecret("other.com/repo", "other_key", "other_value", "did:plc:user2"),
			},
			queryRepo:     DidSlashRepo("did:plc:foo/repo"),
			expectedCount: 1,
			expectedKeys:  []string{"single_key"},
			expectError:   false,
		},
		{
			name: "get secrets for non-existent repo",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "key1", "value1", "did:plc:user1"),
			},
			queryRepo:     DidSlashRepo("nonexistent.com/repo"),
			expectedCount: 0,
			expectedKeys:  []string{},
			expectError:   false,
		},
		{
			name:          "get secrets from empty database",
			setupSecrets:  []UnlockedSecret{},
			queryRepo:     DidSlashRepo("did:plc:foo/repo"),
			expectedCount: 0,
			expectedKeys:  []string{},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := createInMemoryDB(t)
			defer manager.db.Close()

			// Setup secrets
			for _, secret := range tt.setupSecrets {
				if err := manager.AddSecret(secret); err != nil {
					t.Fatalf("Failed to setup secret: %v", err)
				}
			}

			// Test getting locked secrets
			lockedSecrets, err := manager.GetSecretsLocked(tt.queryRepo)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
				return
			}
			if !tt.expectError && err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(lockedSecrets) != tt.expectedCount {
				t.Errorf("Expected %d secrets, got %d", tt.expectedCount, len(lockedSecrets))
			}

			// Verify keys and that values are not present (locked)
			foundKeys := make(map[string]bool)
			for _, ls := range lockedSecrets {
				foundKeys[ls.Key] = true
				if ls.Repo != tt.queryRepo {
					t.Errorf("Expected repo %s, got %s", tt.queryRepo, ls.Repo)
				}
				if ls.CreatedBy == "" {
					t.Error("Expected CreatedBy to be present")
				}
				if ls.CreatedAt.IsZero() {
					t.Error("Expected CreatedAt to be set")
				}
			}

			for _, expectedKey := range tt.expectedKeys {
				if !foundKeys[expectedKey] {
					t.Errorf("Expected key %s not found", expectedKey)
				}
			}
		})
	}
}

func TestSqliteManager_GetSecretsUnlocked(t *testing.T) {
	tests := []struct {
		name            string
		setupSecrets    []UnlockedSecret
		queryRepo       DidSlashRepo
		expectedCount   int
		expectedSecrets map[string]string // key -> value
		expectError     bool
	}{
		{
			name: "get unlocked secrets for repo with multiple secrets",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "key1", "value1", "did:plc:user1"),
				createTestSecret("did:plc:foo/repo", "key2", "value2", "did:plc:user2"),
				createTestSecret("other.com/repo", "key3", "value3", "did:plc:user3"),
			},
			queryRepo:     DidSlashRepo("did:plc:foo/repo"),
			expectedCount: 2,
			expectedSecrets: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			expectError: false,
		},
		{
			name: "get unlocked secrets for repo with single secret",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "single_key", "single_value", "did:plc:user1"),
				createTestSecret("other.com/repo", "other_key", "other_value", "did:plc:user2"),
			},
			queryRepo:     DidSlashRepo("did:plc:foo/repo"),
			expectedCount: 1,
			expectedSecrets: map[string]string{
				"single_key": "single_value",
			},
			expectError: false,
		},
		{
			name: "get unlocked secrets for non-existent repo",
			setupSecrets: []UnlockedSecret{
				createTestSecret("did:plc:foo/repo", "key1", "value1", "did:plc:user1"),
			},
			queryRepo:       DidSlashRepo("nonexistent.com/repo"),
			expectedCount:   0,
			expectedSecrets: map[string]string{},
			expectError:     false,
		},
		{
			name:            "get unlocked secrets from empty database",
			setupSecrets:    []UnlockedSecret{},
			queryRepo:       DidSlashRepo("did:plc:foo/repo"),
			expectedCount:   0,
			expectedSecrets: map[string]string{},
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := createInMemoryDB(t)
			defer manager.db.Close()

			// Setup secrets
			for _, secret := range tt.setupSecrets {
				if err := manager.AddSecret(secret); err != nil {
					t.Fatalf("Failed to setup secret: %v", err)
				}
			}

			// Test getting unlocked secrets
			unlockedSecrets, err := manager.GetSecretsUnlocked(tt.queryRepo)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
				return
			}
			if !tt.expectError && err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(unlockedSecrets) != tt.expectedCount {
				t.Errorf("Expected %d secrets, got %d", tt.expectedCount, len(unlockedSecrets))
			}

			// Verify keys, values, and metadata
			for _, us := range unlockedSecrets {
				expectedValue, exists := tt.expectedSecrets[us.Key]
				if !exists {
					t.Errorf("Unexpected key: %s", us.Key)
					continue
				}
				if us.Value != expectedValue {
					t.Errorf("Expected value %s for key %s, got %s", expectedValue, us.Key, us.Value)
				}
				if us.Repo != tt.queryRepo {
					t.Errorf("Expected repo %s, got %s", tt.queryRepo, us.Repo)
				}
				if us.CreatedBy == "" {
					t.Error("Expected CreatedBy to be present")
				}
				if us.CreatedAt.IsZero() {
					t.Error("Expected CreatedAt to be set")
				}
			}
		})
	}
}

// Test that demonstrates interface usage with table-driven tests
func TestManagerInterface_Usage(t *testing.T) {
	tests := []struct {
		name        string
		operations  []func(Manager) error
		expectError bool
	}{
		{
			name: "successful workflow",
			operations: []func(Manager) error{
				func(m Manager) error {
					secret := createTestSecret("interface.test/repo", "test_key", "test_value", "did:plc:user")
					return m.AddSecret(secret)
				},
				func(m Manager) error {
					_, err := m.GetSecretsLocked(DidSlashRepo("interface.test/repo"))
					return err
				},
				func(m Manager) error {
					_, err := m.GetSecretsUnlocked(DidSlashRepo("interface.test/repo"))
					return err
				},
				func(m Manager) error {
					secret := Secret[any]{
						Key:  "test_key",
						Repo: DidSlashRepo("interface.test/repo"),
					}
					return m.RemoveSecret(secret)
				},
			},
			expectError: false,
		},
		{
			name: "error on duplicate key",
			operations: []func(Manager) error{
				func(m Manager) error {
					secret := createTestSecret("interface.test/repo", "dup_key", "value1", "did:plc:user")
					return m.AddSecret(secret)
				},
				func(m Manager) error {
					secret := createTestSecret("interface.test/repo", "dup_key", "value2", "did:plc:user")
					return m.AddSecret(secret) // Should return ErrKeyAlreadyPresent
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var manager Manager = createInMemoryDB(t)
			defer func() {
				if sqliteManager, ok := manager.(*SqliteManager); ok {
					sqliteManager.db.Close()
				}
			}()

			var finalErr error
			for i, operation := range tt.operations {
				if err := operation(manager); err != nil {
					finalErr = err
					t.Logf("Operation %d returned error: %v", i, err)
				}
			}

			if tt.expectError && finalErr == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && finalErr != nil {
				t.Errorf("Unexpected error: %v", finalErr)
			}
		})
	}
}

// Integration test with table-driven scenarios
func TestSqliteManager_Integration(t *testing.T) {
	tests := []struct {
		name     string
		scenario func(*testing.T, *SqliteManager)
	}{
		{
			name: "multi-repo secret management",
			scenario: func(t *testing.T, manager *SqliteManager) {
				repo1 := DidSlashRepo("example1.com/repo")
				repo2 := DidSlashRepo("example2.com/repo")

				secrets := []UnlockedSecret{
					createTestSecret(string(repo1), "db_password", "super_secret_123", "did:plc:admin"),
					createTestSecret(string(repo1), "api_key", "api_key_456", "did:plc:user1"),
					createTestSecret(string(repo2), "token", "bearer_token_789", "did:plc:user2"),
				}

				// Add all secrets
				for _, secret := range secrets {
					if err := manager.AddSecret(secret); err != nil {
						t.Fatalf("Failed to add secret %s: %v", secret.Key, err)
					}
				}

				// Verify counts
				locked1, _ := manager.GetSecretsLocked(repo1)
				locked2, _ := manager.GetSecretsLocked(repo2)

				if len(locked1) != 2 {
					t.Errorf("Expected 2 secrets for repo1, got %d", len(locked1))
				}
				if len(locked2) != 1 {
					t.Errorf("Expected 1 secret for repo2, got %d", len(locked2))
				}

				// Remove and verify
				secretToRemove := Secret[any]{Key: "db_password", Repo: repo1}
				if err := manager.RemoveSecret(secretToRemove); err != nil {
					t.Fatalf("Failed to remove secret: %v", err)
				}

				locked1After, _ := manager.GetSecretsLocked(repo1)
				if len(locked1After) != 1 {
					t.Errorf("Expected 1 secret for repo1 after removal, got %d", len(locked1After))
				}
				if locked1After[0].Key != "api_key" {
					t.Errorf("Expected remaining secret to be 'api_key', got %s", locked1After[0].Key)
				}
			},
		},
		{
			name: "empty database operations",
			scenario: func(t *testing.T, manager *SqliteManager) {
				repo := DidSlashRepo("empty.test/repo")

				// Operations on empty database should not error
				locked, err := manager.GetSecretsLocked(repo)
				if err != nil {
					t.Errorf("GetSecretsLocked on empty DB failed: %v", err)
				}
				if len(locked) != 0 {
					t.Errorf("Expected 0 secrets, got %d", len(locked))
				}

				unlocked, err := manager.GetSecretsUnlocked(repo)
				if err != nil {
					t.Errorf("GetSecretsUnlocked on empty DB failed: %v", err)
				}
				if len(unlocked) != 0 {
					t.Errorf("Expected 0 secrets, got %d", len(unlocked))
				}

				// Remove from empty should return ErrKeyNotFound
				nonExistent := Secret[any]{Key: "none", Repo: repo}
				err = manager.RemoveSecret(nonExistent)
				if err != ErrKeyNotFound {
					t.Errorf("Expected ErrKeyNotFound, got %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := createInMemoryDB(t)
			defer manager.db.Close()
			tt.scenario(t, manager)
		})
	}
}
