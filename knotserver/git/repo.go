package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

func InitBare(path, defaultBranch string) error {
	parent := filepath.Dir(path)

	if err := os.MkdirAll(parent, 0755); errors.Is(err, os.ErrExist) {
		return fmt.Errorf("error creating user directory: %w", err)
	}

	repository, err := gogit.PlainInit(path, true)
	if err != nil {
		return err
	}

	// set up default branch
	err = repository.CreateBranch(&config.Branch{
		Name: defaultBranch,
	})
	if err != nil {
		return fmt.Errorf("creating branch: %w", err)
	}

	defaultReference := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", defaultBranch))

	ref := plumbing.NewSymbolicReference(plumbing.HEAD, defaultReference)
	if err = repository.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("creating symbolic reference: %w", err)
	}

	return nil
}
