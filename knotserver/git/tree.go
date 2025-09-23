package git

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.org/core/types"
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

var (
	TerminateWalk error = errors.New("terminate walk")
)

type callback = func(node object.TreeEntry, parent *object.Tree, fullPath string) error

func (g *GitRepo) Walk(
	ctx context.Context,
	root string,
	cb callback,
) error {
	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return fmt.Errorf("commit object: %w", err)
	}

	tree, err := c.Tree()
	if err != nil {
		return fmt.Errorf("file tree: %w", err)
	}

	subtree := tree
	if root != "" {
		subtree, err = tree.Tree(root)
		if err != nil {
			return fmt.Errorf("sub tree: %w", err)
		}
	}

	return g.walkHelper(ctx, root, subtree, cb)
}

func (g *GitRepo) walkHelper(
	ctx context.Context,
	root string,
	currentTree *object.Tree,
	cb callback,
) error {
	for _, e := range currentTree.Entries {
		// check if context hits deadline before processing
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		mode, err := e.Mode.ToOSFileMode()
		if err != nil {
			// TODO: log this
			continue
		}

		if e.Mode.IsFile() {
			err = cb(e, currentTree, root)
			if errors.Is(err, TerminateWalk) {
				return err
			}
		}

		// e is a directory
		if mode.IsDir() {
			subtree, err := currentTree.Tree(e.Name)
			if err != nil {
				return fmt.Errorf("sub tree %s: %w", e.Name, err)
			}

			fullPath := path.Join(root, e.Name)

			err = g.walkHelper(ctx, fullPath, subtree, cb)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
