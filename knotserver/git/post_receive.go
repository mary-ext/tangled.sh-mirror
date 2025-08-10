package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"

	"github.com/go-git/go-git/v5/plumbing"
)

type PostReceiveLine struct {
	OldSha plumbing.Hash // old sha of reference being updated
	NewSha plumbing.Hash // new sha of reference being updated
	Ref    string        // the reference being updated
}

func ParsePostReceive(buf io.Reader) ([]PostReceiveLine, error) {
	scanner := bufio.NewScanner(buf)
	var lines []PostReceiveLine
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			continue
		}

		oldSha := parts[0]
		newSha := parts[1]
		ref := parts[2]

		lines = append(lines, PostReceiveLine{
			OldSha: plumbing.NewHash(oldSha),
			NewSha: plumbing.NewHash(newSha),
			Ref:    ref,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}

type RefUpdateMeta struct {
	CommitCount   CommitCount
	IsDefaultRef  bool
	LangBreakdown LangBreakdown
}

type CommitCount struct {
	ByEmail map[string]int
}

func (g *GitRepo) RefUpdateMeta(line PostReceiveLine) (RefUpdateMeta, error) {
	var errs error

	commitCount, err := g.newCommitCount(line)
	errors.Join(errs, err)

	isDefaultRef, err := g.isDefaultBranch(line)
	errors.Join(errs, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	breakdown, err := g.AnalyzeLanguages(ctx)
	errors.Join(errs, err)

	return RefUpdateMeta{
		CommitCount:   commitCount,
		IsDefaultRef:  isDefaultRef,
		LangBreakdown: breakdown,
	}, errs
}

func (g *GitRepo) newCommitCount(line PostReceiveLine) (CommitCount, error) {
	byEmail := make(map[string]int)
	commitCount := CommitCount{
		ByEmail: byEmail,
	}

	if line.NewSha.IsZero() {
		return commitCount, nil
	}

	args := []string{fmt.Sprintf("--max-count=%d", 100)}

	if line.OldSha.IsZero() {
		// git rev-list <newsha> ^other-branches --not ^this-branch
		args = append(args, line.NewSha.String())

		branches, _ := g.Branches()
		for _, b := range branches {
			if !strings.Contains(line.Ref, b.Name) {
				args = append(args, fmt.Sprintf("^%s", b.Name))
			}
		}

		args = append(args, "--not")
		args = append(args, fmt.Sprintf("^%s", line.Ref))
	} else {
		// git rev-list <oldsha>..<newsha>
		args = append(args, fmt.Sprintf("%s..%s", line.OldSha.String(), line.NewSha.String()))
	}

	output, err := g.revList(args...)
	if err != nil {
		return commitCount, fmt.Errorf("failed to run rev-list: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return commitCount, nil
	}

	for _, item := range lines {
		obj, err := g.r.CommitObject(plumbing.NewHash(item))
		if err != nil {
			continue
		}
		commitCount.ByEmail[obj.Author.Email] += 1
	}

	return commitCount, nil
}

func (g *GitRepo) isDefaultBranch(line PostReceiveLine) (bool, error) {
	defaultBranch, err := g.FindMainBranch()
	if err != nil {
		return false, err
	}

	refName := plumbing.ReferenceName(line.Ref)
	if refName.IsBranch() {
		return defaultBranch == refName.Short(), nil
	}

	return false, err
}

func (m RefUpdateMeta) AsRecord() tangled.GitRefUpdate_Meta {
	var byEmail []*tangled.GitRefUpdate_Meta_CommitCount_ByEmail_Elem
	for e, v := range m.CommitCount.ByEmail {
		byEmail = append(byEmail, &tangled.GitRefUpdate_Meta_CommitCount_ByEmail_Elem{
			Email: e,
			Count: int64(v),
		})
	}

	var langs []*tangled.GitRefUpdate_Pair
	for lang, size := range m.LangBreakdown {
		langs = append(langs, &tangled.GitRefUpdate_Pair{
			Lang: lang,
			Size: size,
		})
	}
	langBreakdown := &tangled.GitRefUpdate_Meta_LangBreakdown{
		Inputs: langs,
	}

	return tangled.GitRefUpdate_Meta{
		CommitCount: &tangled.GitRefUpdate_Meta_CommitCount{
			ByEmail: byEmail,
		},
		IsDefaultRef:  m.IsDefaultRef,
		LangBreakdown: langBreakdown,
	}
}
