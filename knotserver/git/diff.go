package git

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.org/core/patchutil"
	"tangled.org/core/types"
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
	nd.Commit.PGPSignature = c.PGPSignature
	nd.Commit.Committer = c.Committer
	nd.Commit.Tree = c.TreeHash.String()

	if parent.Hash.IsZero() {
		nd.Commit.Parent = ""
	} else {
		nd.Commit.Parent = parent.Hash.String()
	}
	nd.Commit.Author = c.Author
	nd.Commit.Message = c.Message

	if v, ok := c.ExtraHeaders["change-id"]; ok {
		nd.Commit.ChangedId = string(v)
	}

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
func (g *GitRepo) formatSinglePatch(commit plumbing.Hash, extraArgs ...string) (string, *types.FormatPatch, error) {
	var stdout bytes.Buffer

	args := []string{
		"-C",
		g.path,
		"format-patch",
		"-1",
		commit.String(),
		"--stdout",
	}
	args = append(args, extraArgs...)

	cmd := exec.Command("git", args...)
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

	if len(formatPatch) > 1 {
		return "", nil, fmt.Errorf("running format-patch on single commit produced more than on patch")
	}

	return stdout.String(), &formatPatch[0], nil
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

func (g *GitRepo) commitsBetween(newCommit, oldCommit *object.Commit) ([]*object.Commit, error) {
	var commits []*object.Commit

	output, err := g.revList(
		"--no-merges", // format-patch explicitly prepares only non-merges
		fmt.Sprintf("%s..%s", oldCommit.Hash.String(), newCommit.Hash.String()),
	)
	if err != nil {
		return nil, fmt.Errorf("revlist: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return commits, nil
	}

	for _, item := range lines {
		obj, err := g.r.CommitObject(plumbing.NewHash(item))
		if err != nil {
			continue
		}
		commits = append(commits, obj)
	}

	return commits, nil
}

func (g *GitRepo) FormatPatch(base, commit2 *object.Commit) (string, []types.FormatPatch, error) {
	// get list of commits between commit2 and base
	commits, err := g.commitsBetween(commit2, base)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get commits: %w", err)
	}

	// reverse the list so we start from the oldest one and go up to the most recent one
	slices.Reverse(commits)

	var allPatchesContent strings.Builder
	var allPatches []types.FormatPatch

	for _, commit := range commits {
		changeId := ""
		if val, ok := commit.ExtraHeaders["change-id"]; ok {
			changeId = string(val)
		}

		var additionalArgs []string
		if changeId != "" {
			additionalArgs = append(additionalArgs, "--add-header", fmt.Sprintf("Change-Id: %s", changeId))
		}

		stdout, patch, err := g.formatSinglePatch(commit.Hash, additionalArgs...)
		if err != nil {
			return "", nil, fmt.Errorf("failed to format patch for commit %s: %w", commit.Hash.String(), err)
		}

		allPatchesContent.WriteString(stdout)
		allPatchesContent.WriteString("\n")

		allPatches = append(allPatches, *patch)
	}

	return allPatchesContent.String(), allPatches, nil
}
