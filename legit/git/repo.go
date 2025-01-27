package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

func InitBare(path string) error {
	parent := filepath.Dir(path)

	if err := os.MkdirAll(parent, 0755); errors.Is(err, os.ErrExist) {
		return fmt.Errorf("error creating user directory: %w", err)
	}

	repository, err := gogit.PlainInit(path, true)
	if err != nil {
		return err
	}

	err = repository.CreateBranch(&config.Branch{
		Name: "main",
	})
	if err != nil {
		return fmt.Errorf("creating branch: %w", err)
	}

	return nil
}
