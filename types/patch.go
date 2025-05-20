package types

import (
	"fmt"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type FormatPatch struct {
	Files []*gitdiff.File
	*gitdiff.PatchHeader
	Raw string
}

func (f FormatPatch) ChangeId() (string, error) {
	if vals, ok := f.RawHeaders["Change-Id"]; ok && len(vals) == 1 {
		return vals[0], nil
	}
	return "", fmt.Errorf("no change-id found")
}
