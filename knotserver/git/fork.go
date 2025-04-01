package git

import (
	"fmt"

	"github.com/go-git/go-git/v5"
)

func Fork(repoPath, source string) error {
	_, err := git.PlainClone(repoPath, true, &git.CloneOptions{
		URL:          source,
		Depth:        1,
		SingleBranch: false,
	})

	if err != nil {
		return fmt.Errorf("failed to bare clone repository: %w", err)
	}
	return nil
}
