package types

import (
	"github.com/go-git/go-git/v5/plumbing/object"
)

// A nicer git tree representation.
type NiceTree struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Size      int64  `json:"size"`
	IsFile    bool   `json:"is_file"`
	IsSubtree bool   `json:"is_subtree"`

	LastCommit *object.Commit `json:"last_commit,omitempty"`
}
