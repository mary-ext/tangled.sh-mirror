package interdiff

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type ReconstructedLine struct {
	LineNumber int64
	Content    string
	IsUnknown  bool
}

func NewLineAt(lineNumber int64, content string) ReconstructedLine {
	return ReconstructedLine{
		LineNumber: lineNumber,
		Content:    content,
		IsUnknown:  false,
	}
}

type ReconstructedFile struct {
	File string
	Data []*ReconstructedLine
}

func (r *ReconstructedFile) String() string {
	var i, j int64
	var b strings.Builder
	for {
		i += 1

		if int(j) >= (len(r.Data)) {
			break
		}

		if r.Data[j].LineNumber == i {
			// b.WriteString(fmt.Sprintf("%d:", r.Data[j].LineNumber))
			b.WriteString(r.Data[j].Content)
			j += 1
		} else {
			//b.WriteString(fmt.Sprintf("%d:\n", i))
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (r *ReconstructedFile) AddLine(line *ReconstructedLine) {
	r.Data = append(r.Data, line)
}

func bestName(file *gitdiff.File) string {
	if file.IsDelete {
		return file.OldName
	} else {
		return file.NewName
	}
}

// rebuild the original file from a patch
func CreateOriginal(file *gitdiff.File) ReconstructedFile {
	rf := ReconstructedFile{
		File: bestName(file),
	}

	for _, fragment := range file.TextFragments {
		position := fragment.OldPosition
		for _, line := range fragment.Lines {
			switch line.Op {
			case gitdiff.OpContext:
				rl := NewLineAt(position, line.Line)
				rf.Data = append(rf.Data, &rl)
				position += 1
			case gitdiff.OpDelete:
				rl := NewLineAt(position, line.Line)
				rf.Data = append(rf.Data, &rl)
				position += 1
			case gitdiff.OpAdd:
				// do nothing here
			}
		}
	}

	return rf
}

type MergeError struct {
	msg              string
	mismatchingLines []int64
}

func (m MergeError) Error() string {
	return fmt.Sprintf("%s: %v", m.msg, m.mismatchingLines)
}

// best effort merging of two reconstructed files
func (this *ReconstructedFile) Merge(other *ReconstructedFile) (*ReconstructedFile, error) {
	mismatchingLines := []int64{}
	mergedFile := ReconstructedFile{}

	var i, j int64

	for int(i) < len(this.Data) || int(j) < len(other.Data) {
		if int(i) >= len(this.Data) {
			// first file is done; the rest of the lines from file 2 can go in
			mergedFile.AddLine(other.Data[j])
			j++
			continue
		}

		if int(j) >= len(other.Data) {
			// first file is done; the rest of the lines from file 2 can go in
			mergedFile.AddLine(this.Data[i])
			i++
			continue
		}

		line1 := this.Data[i]
		line2 := other.Data[j]

		if line1.LineNumber == line2.LineNumber {
			if line1.Content != line2.Content {
				mismatchingLines = append(mismatchingLines, line1.LineNumber)
			} else {
				mergedFile.AddLine(line1)
				i++
				j++
			}
		} else if line1.LineNumber < line2.LineNumber {
			mergedFile.AddLine(line1)
			i++
		} else {
			mergedFile.AddLine(line2)
			j++
		}
	}

	if len(mismatchingLines) > 0 {
		return nil, MergeError{
			msg:              "mismatching lines; this patch might have undergone rebase",
			mismatchingLines: mismatchingLines,
		}
	} else {
		return &mergedFile, nil
	}
}

func (r *ReconstructedFile) Apply(patch *gitdiff.File) (string, error) {
	original := r.String()
	var buffer bytes.Buffer
	reader := strings.NewReader(original)

	err := gitdiff.Apply(&buffer, reader, patch)
	if err != nil {
		return "", err
	}

	return buffer.String(), nil
}

func Unified(oldText, oldFile, newText, newFile string) (string, error) {
	oldTemp, err := os.CreateTemp("", "old_*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file for oldText: %w", err)
	}
	defer os.Remove(oldTemp.Name())
	if _, err := oldTemp.WriteString(oldText); err != nil {
		return "", fmt.Errorf("failed to write to old temp file: %w", err)
	}
	oldTemp.Close()

	newTemp, err := os.CreateTemp("", "new_*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file for newText: %w", err)
	}
	defer os.Remove(newTemp.Name())
	if _, err := newTemp.WriteString(newText); err != nil {
		return "", fmt.Errorf("failed to write to new temp file: %w", err)
	}
	newTemp.Close()

	cmd := exec.Command("diff", "-u", "--label", oldFile, "--label", newFile, oldTemp.Name(), newTemp.Name())
	output, err := cmd.CombinedOutput()

	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return string(output), nil
	}
	if err != nil {
		return "", fmt.Errorf("diff command failed: %w", err)
	}

	return string(output), nil
}

type InterdiffResult struct {
	Files []*InterdiffFile
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
	re1 := CreateOriginal(f1)
	re2 := CreateOriginal(f2)
	var interdiffFile InterdiffFile
	var status InterdiffFileStatus

	merged, err := re1.Merge(&re2)
	if err != nil {
		status = InterdiffFileStatus{
			StatusKind: StatusRebased,
			Error:      err,
		}
	}

	rev1, err := merged.Apply(f1)
	if err != nil {
		status = InterdiffFileStatus{
			StatusKind: StatusError,
			Error:      err,
		}
	}

	rev2, err := merged.Apply(f2)
	if err != nil {
		status = InterdiffFileStatus{
			StatusKind: StatusError,
			Error:      err,
		}
	}

	diff, err := Unified(rev1, bestName(f1), rev2, bestName(f2))
	if err != nil {
		status = InterdiffFileStatus{
			StatusKind: StatusError,
			Error:      err,
		}
	}

	parsed, _, err := gitdiff.Parse(strings.NewReader(diff))
	if err != nil {
		status = InterdiffFileStatus{
			StatusKind: StatusError,
			Error:      err,
		}
	}

	if len(parsed) != 1 {
		// files are identical?
		status = InterdiffFileStatus{
			StatusKind: StatusUnchanged,
		}
	}

	interdiffFile.Status = status
	interdiffFile.Name = bestName(f1)

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
			// only in patch 1
			interdiffFile = &InterdiffFile{
				File: f1,
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
			Status: InterdiffFileStatus{
				StatusKind: StatusOnlyInTwo,
			},
		})
	}

	return &result
}
