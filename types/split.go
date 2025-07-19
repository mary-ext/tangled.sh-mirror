package types

import (
	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type SplitLine struct {
	LineNumber int            `json:"line_number,omitempty"`
	Content    string         `json:"content"`
	Op         gitdiff.LineOp `json:"op"`
	IsEmpty    bool           `json:"is_empty"`
}

type SplitFragment struct {
	Header     string      `json:"header"`
	LeftLines  []SplitLine `json:"left_lines"`
	RightLines []SplitLine `json:"right_lines"`
}

type SplitDiff struct {
	Name          string          `json:"name"`
	TextFragments []SplitFragment `json:"fragments"`
}

// used by html elements as a unique ID for hrefs
func (d *SplitDiff) Id() string {
	return d.Name
}

// separate lines into left and right, this includes additional logic to
// group consecutive runs of additions and deletions in order to align them
// properly in the final output
//
// TODO: move all diff stuff to a single package, we are spread across patchutil and types right now
func SeparateLines(fragment *gitdiff.TextFragment) ([]SplitLine, []SplitLine) {
	lines := fragment.Lines
	var leftLines, rightLines []SplitLine
	oldLineNum := fragment.OldPosition
	newLineNum := fragment.OldPosition

	// process deletions and additions in groups for better alignment
	i := 0
	for i < len(lines) {
		line := lines[i]

		switch line.Op {
		case gitdiff.OpContext:
			leftLines = append(leftLines, SplitLine{
				LineNumber: int(oldLineNum),
				Content:    line.Line,
				Op:         gitdiff.OpContext,
				IsEmpty:    false,
			})
			rightLines = append(rightLines, SplitLine{
				LineNumber: int(newLineNum),
				Content:    line.Line,
				Op:         gitdiff.OpContext,
				IsEmpty:    false,
			})
			oldLineNum++
			newLineNum++
			i++

		case gitdiff.OpDelete:
			deletionCount := 0
			for j := i; j < len(lines) && lines[j].Op == gitdiff.OpDelete; j++ {
				leftLines = append(leftLines, SplitLine{
					LineNumber: int(oldLineNum),
					Content:    lines[j].Line,
					Op:         gitdiff.OpDelete,
					IsEmpty:    false,
				})
				oldLineNum++
				deletionCount++
			}
			i += deletionCount

			additionCount := 0
			for j := i; j < len(lines) && lines[j].Op == gitdiff.OpAdd; j++ {
				rightLines = append(rightLines, SplitLine{
					LineNumber: int(newLineNum),
					Content:    lines[j].Line,
					Op:         gitdiff.OpAdd,
					IsEmpty:    false,
				})
				newLineNum++
				additionCount++
			}
			i += additionCount

			// add empty lines to balance the sides
			if deletionCount > additionCount {
				// more deletions than additions - pad right side
				for k := 0; k < deletionCount-additionCount; k++ {
					rightLines = append(rightLines, SplitLine{
						Content: "",
						Op:      gitdiff.OpContext,
						IsEmpty: true,
					})
				}
			} else if additionCount > deletionCount {
				// more additions than deletions - pad left side
				for k := 0; k < additionCount-deletionCount; k++ {
					leftLines = append(leftLines, SplitLine{
						Content: "",
						Op:      gitdiff.OpContext,
						IsEmpty: true,
					})
				}
			}

		case gitdiff.OpAdd:
			// standalone addition (not preceded by deletion)
			leftLines = append(leftLines, SplitLine{
				Content: "",
				Op:      gitdiff.OpContext,
				IsEmpty: true,
			})
			rightLines = append(rightLines, SplitLine{
				LineNumber: int(newLineNum),
				Content:    line.Line,
				Op:         gitdiff.OpAdd,
				IsEmpty:    false,
			})
			newLineNum++
			i++
		}
	}

	return leftLines, rightLines
}
