package patchutil

import (
	"fmt"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"tangled.org/core/types"
)

type InterdiffResult struct {
	Files []*InterdiffFile
}

func (i *InterdiffResult) AffectedFiles() []string {
	files := make([]string, len(i.Files))
	for _, f := range i.Files {
		files = append(files, f.Name)
	}
	return files
}

func (i *InterdiffResult) String() string {
	var b strings.Builder
	for _, f := range i.Files {
		b.WriteString(f.String())
		b.WriteString("\n")
	}

	return b.String()
}

type InterdiffFile struct {
	*gitdiff.File
	Name   string
	Status InterdiffFileStatus
}

func (s *InterdiffFile) Split() *types.SplitDiff {
	fragments := make([]types.SplitFragment, len(s.TextFragments))

	for i, fragment := range s.TextFragments {
		leftLines, rightLines := types.SeparateLines(fragment)

		fragments[i] = types.SplitFragment{
			Header:     fragment.Header(),
			LeftLines:  leftLines,
			RightLines: rightLines,
		}
	}

	return &types.SplitDiff{
		Name:          s.Id(),
		TextFragments: fragments,
	}
}

// used by html elements as a unique ID for hrefs
func (s *InterdiffFile) Id() string {
	return s.Name
}

func (s *InterdiffFile) String() string {
	var b strings.Builder
	b.WriteString(s.Status.String())
	b.WriteString(" ")

	if s.File != nil {
		b.WriteString(bestName(s.File))
		b.WriteString("\n")
		b.WriteString(s.File.String())
	}

	return b.String()
}

type InterdiffFileStatus struct {
	StatusKind StatusKind
	Error      error
}

func (s *InterdiffFileStatus) String() string {
	kind := s.StatusKind.String()
	if s.Error != nil {
		return fmt.Sprintf("%s [%s]", kind, s.Error.Error())
	} else {
		return kind
	}
}

func (s *InterdiffFileStatus) IsOk() bool {
	return s.StatusKind == StatusOk
}

func (s *InterdiffFileStatus) IsUnchanged() bool {
	return s.StatusKind == StatusUnchanged
}

func (s *InterdiffFileStatus) IsOnlyInOne() bool {
	return s.StatusKind == StatusOnlyInOne
}

func (s *InterdiffFileStatus) IsOnlyInTwo() bool {
	return s.StatusKind == StatusOnlyInTwo
}

func (s *InterdiffFileStatus) IsRebased() bool {
	return s.StatusKind == StatusRebased
}

func (s *InterdiffFileStatus) IsError() bool {
	return s.StatusKind == StatusError
}

type StatusKind int

func (k StatusKind) String() string {
	switch k {
	case StatusOnlyInOne:
		return "only in one"
	case StatusOnlyInTwo:
		return "only in two"
	case StatusUnchanged:
		return "unchanged"
	case StatusRebased:
		return "rebased"
	case StatusError:
		return "error"
	default:
		return "changed"
	}
}

const (
	StatusOk StatusKind = iota
	StatusOnlyInOne
	StatusOnlyInTwo
	StatusUnchanged
	StatusRebased
	StatusError
)

func interdiffFiles(f1, f2 *gitdiff.File) *InterdiffFile {
	re1 := CreatePreImage(f1)
	re2 := CreatePreImage(f2)

	interdiffFile := InterdiffFile{
		Name: bestName(f1),
	}

	merged, err := re1.Merge(&re2)
	if err != nil {
		interdiffFile.Status = InterdiffFileStatus{
			StatusKind: StatusRebased,
			Error:      err,
		}
		return &interdiffFile
	}

	rev1, err := merged.Apply(f1)
	if err != nil {
		interdiffFile.Status = InterdiffFileStatus{
			StatusKind: StatusError,
			Error:      err,
		}
		return &interdiffFile
	}

	rev2, err := merged.Apply(f2)
	if err != nil {
		interdiffFile.Status = InterdiffFileStatus{
			StatusKind: StatusError,
			Error:      err,
		}
		return &interdiffFile
	}

	diff, err := Unified(rev1, bestName(f1), rev2, bestName(f2))
	if err != nil {
		interdiffFile.Status = InterdiffFileStatus{
			StatusKind: StatusError,
			Error:      err,
		}
		return &interdiffFile
	}

	parsed, _, err := gitdiff.Parse(strings.NewReader(diff))
	if err != nil {
		interdiffFile.Status = InterdiffFileStatus{
			StatusKind: StatusError,
			Error:      err,
		}
		return &interdiffFile
	}

	if len(parsed) != 1 {
		// files are identical?
		interdiffFile.Status = InterdiffFileStatus{
			StatusKind: StatusUnchanged,
		}
		return &interdiffFile
	}

	if interdiffFile.Status.StatusKind == StatusOk {
		interdiffFile.File = parsed[0]
	}

	return &interdiffFile
}

func Interdiff(patch1, patch2 []*gitdiff.File) *InterdiffResult {
	fileToIdx1 := make(map[string]int)
	fileToIdx2 := make(map[string]int)
	visited := make(map[string]struct{})
	var result InterdiffResult

	for idx, f := range patch1 {
		fileToIdx1[bestName(f)] = idx
	}

	for idx, f := range patch2 {
		fileToIdx2[bestName(f)] = idx
	}

	for _, f1 := range patch1 {
		var interdiffFile *InterdiffFile

		fileName := bestName(f1)
		if idx, ok := fileToIdx2[fileName]; ok {
			f2 := patch2[idx]

			// we have f1 and f2, calculate interdiff
			interdiffFile = interdiffFiles(f1, f2)
		} else {
			// only in patch 1, this change would have to be "inverted" to dissapear
			// from patch 2, so we reverseDiff(f1)
			reverseDiff(f1)

			interdiffFile = &InterdiffFile{
				File: f1,
				Name: fileName,
				Status: InterdiffFileStatus{
					StatusKind: StatusOnlyInOne,
				},
			}
		}

		result.Files = append(result.Files, interdiffFile)
		visited[fileName] = struct{}{}
	}

	// for all files in patch2 that remain unvisited; we can just add them into the output
	for _, f2 := range patch2 {
		fileName := bestName(f2)
		if _, ok := visited[fileName]; ok {
			continue
		}

		result.Files = append(result.Files, &InterdiffFile{
			File: f2,
			Name: fileName,
			Status: InterdiffFileStatus{
				StatusKind: StatusOnlyInTwo,
			},
		})
	}

	return &result
}
