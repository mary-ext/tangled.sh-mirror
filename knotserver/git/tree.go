package git

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.sh/tangled.sh/core/types"
)

func (g *GitRepo) FileTree(ctx context.Context, path string) ([]types.NiceTree, error) {
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
		files = g.makeNiceTree(ctx, tree, "")
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

			files = g.makeNiceTree(ctx, subtree, path)
		}
	}

	return files, nil
}

func (g *GitRepo) makeNiceTree(ctx context.Context, subtree *object.Tree, parent string) []types.NiceTree {
	nts := []types.NiceTree{}

	times, err := g.calculateCommitTimeIn(ctx, subtree, parent, 2*time.Second)
	if err != nil {
		return nts
	}

	for _, e := range subtree.Entries {
		mode, _ := e.Mode.ToOSFileMode()
		sz, _ := subtree.Size(e.Name)

		fpath := path.Join(parent, e.Name)

		var lastCommit *types.LastCommitInfo
		if t, ok := times[fpath]; ok {
			lastCommit = &types.LastCommitInfo{
				Hash:    t.hash,
				Message: t.message,
				When:    t.when,
			}
		}

		nts = append(nts, types.NiceTree{
			Name:       e.Name,
			Mode:       mode.String(),
			IsFile:     e.Mode.IsFile(),
			Size:       sz,
			LastCommit: lastCommit,
		})

	}

	return nts
}
