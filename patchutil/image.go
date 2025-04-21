package patchutil

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type Line struct {
	LineNumber int64
	Content    string
	IsUnknown  bool
}

func NewLineAt(lineNumber int64, content string) Line {
	return Line{
		LineNumber: lineNumber,
		Content:    content,
		IsUnknown:  false,
	}
}

type Image struct {
	File string
	Data []*Line
}

func (r *Image) String() string {
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

func (r *Image) AddLine(line *Line) {
	r.Data = append(r.Data, line)
}

// rebuild the original file from a patch
func CreatePreImage(file *gitdiff.File) Image {
	rf := Image{
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

// rebuild the revised file from a patch
func CreatePostImage(file *gitdiff.File) Image {
	rf := Image{
		File: bestName(file),
	}

	for _, fragment := range file.TextFragments {
		position := fragment.NewPosition
		for _, line := range fragment.Lines {
			switch line.Op {
			case gitdiff.OpContext:
				rl := NewLineAt(position, line.Line)
				rf.Data = append(rf.Data, &rl)
				position += 1
			case gitdiff.OpAdd:
				rl := NewLineAt(position, line.Line)
				rf.Data = append(rf.Data, &rl)
				position += 1
			case gitdiff.OpDelete:
				// do nothing here
			}
		}
	}

	return rf
}

type MergeError struct {
	msg             string
	mismatchingLine int64
}

func (m MergeError) Error() string {
	return fmt.Sprintf("%s: %v", m.msg, m.mismatchingLine)
}

// best effort merging of two reconstructed files
func (this *Image) Merge(other *Image) (*Image, error) {
	mergedFile := Image{}

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
				return nil, MergeError{
					msg:             "mismatching lines, this patch might have undergone rebase",
					mismatchingLine: line1.LineNumber,
				}
			} else {
				mergedFile.AddLine(line1)
			}
			i++
			j++
		} else if line1.LineNumber < line2.LineNumber {
			mergedFile.AddLine(line1)
			i++
		} else {
			mergedFile.AddLine(line2)
			j++
		}
	}

	return &mergedFile, nil
}

func (r *Image) Apply(patch *gitdiff.File) (string, error) {
	original := r.String()
	var buffer bytes.Buffer
	reader := strings.NewReader(original)

	err := gitdiff.Apply(&buffer, reader, patch)
	if err != nil {
		return "", err
	}

	return buffer.String(), nil
}
