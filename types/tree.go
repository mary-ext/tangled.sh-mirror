package types

import (
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// A nicer git tree representation.
type NiceTree struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Size      int64  `json:"size"`
	IsFile    bool   `json:"is_file"`
	IsSubtree bool   `json:"is_subtree"`

	LastCommit *LastCommitInfo `json:"last_commit,omitempty"`
}

type LastCommitInfo struct {
	Hash    plumbing.Hash
	Message string
	When    time.Time
}
