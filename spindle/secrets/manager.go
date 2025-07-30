package secrets

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type DidSlashRepo string

type Secret[T any] struct {
	Key       string
	Value     T
	Repo      DidSlashRepo
	CreatedAt time.Time
	CreatedBy syntax.DID
}

// the secret is not present
type LockedSecret = Secret[struct{}]

// the secret is present in plaintext, never expose this publicly,
// only use in the workflow engine
type UnlockedSecret = Secret[string]

type Manager interface {
	AddSecret(ctx context.Context, secret UnlockedSecret) error
	RemoveSecret(ctx context.Context, secret Secret[any]) error
	GetSecretsLocked(ctx context.Context, repo DidSlashRepo) ([]LockedSecret, error)
	GetSecretsUnlocked(ctx context.Context, repo DidSlashRepo) ([]UnlockedSecret, error)
}

// stopper interface for managers that need cleanup
type Stopper interface {
	Stop()
}

var ErrKeyAlreadyPresent = errors.New("key already present")
var ErrInvalidKeyIdent = errors.New("key is not a valid identifier")
var ErrKeyNotFound = errors.New("key not found")

// ensure that we are satisfying the interface
var (
	_ = []Manager{
		&SqliteManager{},
		&OpenBaoManager{},
	}
)

var (
	// bash identifier syntax
	keyIdent = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

func isValidKey(key string) bool {
	if key == "" {
		return false
	}
	return keyIdent.MatchString(key)
}

func ValidateKey(key string) error {
	if !isValidKey(key) {
		return ErrInvalidKeyIdent
	}
	return nil
}
