package git

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sotangled/tangled/types"
)

func (g *GitRepo) FileTree(path string) ([]types.NiceTree, error) {
	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return nil, fmt.Errorf("commit object: %w", err)
	}

	files := []types.NiceTree{}
	tree, err := c.Tree()
	if err != nil {
		return nil, fmt.Errorf("file tree: %w", err)
	}

	if path == "" {
		files = makeNiceTree(tree)
	} else {
		o, err := tree.FindEntry(path)
		if err != nil {
			return nil, err
		}

		if !o.Mode.IsFile() {
			subtree, err := tree.Tree(path)
			if err != nil {
				return nil, err
			}

			files = makeNiceTree(subtree)
		}
	}

	return files, nil
}

func makeNiceTree(t *object.Tree) []types.NiceTree {
	nts := []types.NiceTree{}

	for _, e := range t.Entries {
		mode, _ := e.Mode.ToOSFileMode()
		sz, _ := t.Size(e.Name)
		nts = append(nts, types.NiceTree{
			Name:   e.Name,
			Mode:   mode.String(),
			IsFile: e.Mode.IsFile(),
			Size:   sz,
		})
	}

	return nts
}
