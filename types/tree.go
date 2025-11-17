package types

import (
	"fmt"
	"os"
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
	if numericMode, err := filemode.New(t.Mode); err == nil {
		return numericMode, nil
	}

	// TODO: this is here for backwards compat, can be removed in future versions
	osMode, err := parseModeString(t.Mode)
	if err != nil {
		return filemode.Empty, nil
	}

	conv, err := filemode.NewFromOSFileMode(osMode)
	if err != nil {
		return filemode.Empty, nil
	}

	return conv, nil
}

// ParseFileModeString parses a file mode string like "-rw-r--r--"
// and returns an os.FileMode
func parseModeString(modeStr string) (os.FileMode, error) {
	if len(modeStr) != 10 {
		return 0, fmt.Errorf("invalid mode string length: expected 10, got %d", len(modeStr))
	}

	var mode os.FileMode

	// Parse file type (first character)
	switch modeStr[0] {
	case 'd':
		mode |= os.ModeDir
	case 'l':
		mode |= os.ModeSymlink
	case '-':
		// regular file
	default:
		return 0, fmt.Errorf("unknown file type: %c", modeStr[0])
	}

	// parse permissions for owner, group, and other
	perms := modeStr[1:]
	shifts := []int{6, 3, 0} // bit shifts for owner, group, other

	for i := range 3 {
		offset := i * 3
		shift := shifts[i]

		if perms[offset] == 'r' {
			mode |= os.FileMode(4 << shift)
		}
		if perms[offset+1] == 'w' {
			mode |= os.FileMode(2 << shift)
		}
		if perms[offset+2] == 'x' {
			mode |= os.FileMode(1 << shift)
		}
	}

	return mode, nil
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
