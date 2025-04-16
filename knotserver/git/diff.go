package git

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/types"
)

func (g *GitRepo) Diff() (*types.NiceDiff, error) {
	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return nil, fmt.Errorf("commit object: %w", err)
	}

	patch := &object.Patch{}
	commitTree, err := c.Tree()
	parent := &object.Commit{}
	if err == nil {
		parentTree := &object.Tree{}
		if c.NumParents() != 0 {
			parent, err = c.Parents().Next()
			if err == nil {
				parentTree, err = parent.Tree()
				if err == nil {
					patch, err = parentTree.Patch(commitTree)
					if err != nil {
						return nil, fmt.Errorf("patch: %w", err)
					}
				}
			}
		} else {
			patch, err = parentTree.Patch(commitTree)
			if err != nil {
				return nil, fmt.Errorf("patch: %w", err)
			}
		}
	}

	diffs, _, err := gitdiff.Parse(strings.NewReader(patch.String()))
	if err != nil {
		log.Println(err)
	}

	nd := types.NiceDiff{}
	for _, d := range diffs {
		ndiff := types.Diff{}
		ndiff.Name.New = d.NewName
		ndiff.Name.Old = d.OldName
		ndiff.IsBinary = d.IsBinary
		ndiff.IsNew = d.IsNew
		ndiff.IsDelete = d.IsDelete
		ndiff.IsCopy = d.IsCopy
		ndiff.IsRename = d.IsRename

		for _, tf := range d.TextFragments {
			ndiff.TextFragments = append(ndiff.TextFragments, *tf)
			for _, l := range tf.Lines {
				switch l.Op {
				case gitdiff.OpAdd:
					nd.Stat.Insertions += 1
				case gitdiff.OpDelete:
					nd.Stat.Deletions += 1
				}
			}
		}

		nd.Diff = append(nd.Diff, ndiff)
	}

	nd.Stat.FilesChanged = len(diffs)
	nd.Commit.This = c.Hash.String()

	if parent.Hash.IsZero() {
		nd.Commit.Parent = ""
	} else {
		nd.Commit.Parent = parent.Hash.String()
	}
	nd.Commit.Author = c.Author
	nd.Commit.Message = c.Message

	return &nd, nil
}

func (g *GitRepo) DiffTree(commit1, commit2 *object.Commit) (*types.DiffTree, error) {
	tree1, err := commit1.Tree()
	if err != nil {
		return nil, err
	}

	tree2, err := commit2.Tree()
	if err != nil {
		return nil, err
	}

	diff, err := object.DiffTree(tree1, tree2)
	if err != nil {
		return nil, err
	}

	patch, err := diff.Patch()
	if err != nil {
		return nil, err
	}

	diffs, _, err := gitdiff.Parse(strings.NewReader(patch.String()))
	if err != nil {
		return nil, err
	}

	return &types.DiffTree{
		Rev1:  commit1.Hash.String(),
		Rev2:  commit2.Hash.String(),
		Patch: patch.String(),
		Diff:  diffs,
	}, nil
}

// FormatPatch generates a git-format-patch output between two commits,
// and returns the raw format-patch series, a parsed FormatPatch and an error.
func (g *GitRepo) FormatPatch(base, commit2 *object.Commit) (string, []patchutil.FormatPatch, error) {
	var stdout bytes.Buffer
	cmd := exec.Command(
		"git",
		"-C",
		g.path,
		"format-patch",
		fmt.Sprintf("%s..%s", base.Hash.String(), commit2.Hash.String()),
		"--stdout",
	)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return "", nil, err
	}

	formatPatch, err := patchutil.ExtractPatches(stdout.String())
	if err != nil {
		return "", nil, err
	}

	return stdout.String(), formatPatch, nil
}

func (g *GitRepo) MergeBase(commit1, commit2 *object.Commit) (*object.Commit, error) {
	isAncestor, err := commit1.IsAncestor(commit2)
	if err != nil {
		return nil, err
	}

	if isAncestor {
		return commit1, nil
	}

	mergeBase, err := commit1.MergeBase(commit2)
	if err != nil {
		return nil, err
	}

	if len(mergeBase) == 0 {
		return nil, fmt.Errorf("failed to find a merge-base")
	}

	return mergeBase[0], nil
}

func (g *GitRepo) ResolveRevision(revStr string) (*object.Commit, error) {
	rev, err := g.r.ResolveRevision(plumbing.Revision(revStr))
	if err != nil {
		return nil, fmt.Errorf("resolving revision %s: %w", revStr, err)
	}

	commit, err := g.r.CommitObject(*rev)
	if err != nil {

		return nil, fmt.Errorf("getting commit for %s: %w", revStr, err)
	}

	return commit, nil
}
