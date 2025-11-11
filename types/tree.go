package types

import (
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
)

// A nicer git tree representation.
type NiceTree struct {
	// Relative path
	Name string `json:"name"`
	Mode string `json:"mode"`
	Size int64  `json:"size"`

	LastCommit *LastCommitInfo `json:"last_commit,omitempty"`
}

func (t *NiceTree) FileMode() (filemode.FileMode, error) {
	return filemode.New(t.Mode)
}

func (t *NiceTree) IsFile() bool {
	m, err := t.FileMode()

	if err != nil {
		return false
	}

	return m.IsFile()
}

func (t *NiceTree) IsSubmodule() bool {
	m, err := t.FileMode()

	if err != nil {
		return false
	}

	return m == filemode.Submodule
}

type LastCommitInfo struct {
	Hash    plumbing.Hash
	Message string
	When    time.Time
}
