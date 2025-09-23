package patchutil

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"tangled.org/core/types"
)

func ExtractPatches(formatPatch string) ([]types.FormatPatch, error) {
	patches := splitFormatPatch(formatPatch)

	result := []types.FormatPatch{}

	for _, patch := range patches {
		files, headerStr, err := gitdiff.Parse(strings.NewReader(patch))
		if err != nil {
			return nil, fmt.Errorf("failed to parse patch: %w", err)
		}

		header, err := gitdiff.ParsePatchHeader(headerStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse patch header: %w", err)
		}

		result = append(result, types.FormatPatch{
			Files:       files,
			PatchHeader: header,
			Raw:         patch,
		})
	}

	return result, nil
}

// IsPatchValid checks if the given patch string is valid.
// It performs very basic sniffing for either git-diff or git-format-patch
// header lines. For format patches, it attempts to extract and validate each one.
func IsPatchValid(patch string) bool {
	if len(patch) == 0 {
		return false
	}

	lines := strings.Split(patch, "\n")
	if len(lines) < 2 {
		return false
	}

	firstLine := strings.TrimSpace(lines[0])

	// check if it's a git diff
	if strings.HasPrefix(firstLine, "diff ") ||
		strings.HasPrefix(firstLine, "--- ") ||
		strings.HasPrefix(firstLine, "Index: ") ||
		strings.HasPrefix(firstLine, "+++ ") ||
		strings.HasPrefix(firstLine, "@@ ") {
		return true
	}

	// check if it's format-patch
	if strings.HasPrefix(firstLine, "From ") && strings.Contains(firstLine, " Mon Sep 17 00:00:00 2001") ||
		strings.HasPrefix(firstLine, "From: ") {
		// ExtractPatches already runs it through gitdiff.Parse so if that errors,
		// it's safe to say it's broken.
		patches, err := ExtractPatches(patch)
		if err != nil {
			return false
		}
		return len(patches) > 0
	}

	return false
}

func IsFormatPatch(patch string) bool {
	lines := strings.Split(patch, "\n")
	if len(lines) < 2 {
		return false
	}

	firstLine := strings.TrimSpace(lines[0])
	if strings.HasPrefix(firstLine, "From ") && strings.Contains(firstLine, " Mon Sep 17 00:00:00 2001") {
		return true
	}

	headerCount := 0
	for i := range min(10, len(lines)) {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "From: ") ||
			strings.HasPrefix(line, "Date: ") ||
			strings.HasPrefix(line, "Subject: ") ||
			strings.HasPrefix(line, "commit ") {
			headerCount++
		}
	}

	return headerCount >= 2
}

func splitFormatPatch(patchText string) []string {
	re := regexp.MustCompile(`(?m)^From [0-9a-f]{40} .*$`)

	indexes := re.FindAllStringIndex(patchText, -1)

	if len(indexes) == 0 {
		return []string{}
	}

	patches := make([]string, len(indexes))

	for i := range indexes {
		startPos := indexes[i][0]
		endPos := len(patchText)

		if i < len(indexes)-1 {
			endPos = indexes[i+1][0]
		}

		patches[i] = strings.TrimSpace(patchText[startPos:endPos])
	}
	return patches
}

func bestName(file *gitdiff.File) string {
	if file.IsDelete {
		return file.OldName
	} else {
		return file.NewName
	}
}

// in-place reverse of a diff
func reverseDiff(file *gitdiff.File) {
	file.OldName, file.NewName = file.NewName, file.OldName
	file.OldMode, file.NewMode = file.NewMode, file.OldMode
	file.BinaryFragment, file.ReverseBinaryFragment = file.ReverseBinaryFragment, file.BinaryFragment

	for _, fragment := range file.TextFragments {
		// swap postions
		fragment.OldPosition, fragment.NewPosition = fragment.NewPosition, fragment.OldPosition
		fragment.OldLines, fragment.NewLines = fragment.NewLines, fragment.OldLines
		fragment.LinesAdded, fragment.LinesDeleted = fragment.LinesDeleted, fragment.LinesAdded

		for i := range fragment.Lines {
			switch fragment.Lines[i].Op {
			case gitdiff.OpAdd:
				fragment.Lines[i].Op = gitdiff.OpDelete
			case gitdiff.OpDelete:
				fragment.Lines[i].Op = gitdiff.OpAdd
			default:
				// do nothing
			}
		}
	}
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

// are two patches identical
func Equal(a, b []*gitdiff.File) bool {
	return slices.EqualFunc(a, b, func(x, y *gitdiff.File) bool {
		// same pointer
		if x == y {
			return true
		}
		if x == nil || y == nil {
			return x == y
		}

		// compare file metadata
		if x.OldName != y.OldName || x.NewName != y.NewName {
			return false
		}
		if x.OldMode != y.OldMode || x.NewMode != y.NewMode {
			return false
		}
		if x.IsNew != y.IsNew || x.IsDelete != y.IsDelete || x.IsCopy != y.IsCopy || x.IsRename != y.IsRename {
			return false
		}

		if len(x.TextFragments) != len(y.TextFragments) {
			return false
		}

		for i, xFrag := range x.TextFragments {
			yFrag := y.TextFragments[i]

			// Compare fragment headers
			if xFrag.OldPosition != yFrag.OldPosition || xFrag.OldLines != yFrag.OldLines ||
				xFrag.NewPosition != yFrag.NewPosition || xFrag.NewLines != yFrag.NewLines {
				return false
			}

			// Compare fragment changes
			if len(xFrag.Lines) != len(yFrag.Lines) {
				return false
			}

			for j, xLine := range xFrag.Lines {
				yLine := yFrag.Lines[j]
				if xLine.Op != yLine.Op || xLine.Line != yLine.Line {
					return false
				}
			}
		}

		return true
	})
}

// sort patch files in alphabetical order
func SortPatch(patch []*gitdiff.File) {
	slices.SortFunc(patch, func(a, b *gitdiff.File) int {
		return strings.Compare(bestName(a), bestName(b))
	})
}

func AsDiff(patch string) ([]*gitdiff.File, error) {
	// if format-patch; then extract each patch
	var diffs []*gitdiff.File
	if IsFormatPatch(patch) {
		patches, err := ExtractPatches(patch)
		if err != nil {
			return nil, err
		}
		var ps [][]*gitdiff.File
		for _, p := range patches {
			ps = append(ps, p.Files)
		}

		diffs = CombineDiff(ps...)
	} else {
		d, _, err := gitdiff.Parse(strings.NewReader(patch))
		if err != nil {
			return nil, err
		}
		diffs = d
	}

	return diffs, nil
}

func AsNiceDiff(patch, targetBranch string) types.NiceDiff {
	diffs, err := AsDiff(patch)
	if err != nil {
		log.Println(err)
	}

	nd := types.NiceDiff{}
	nd.Commit.Parent = targetBranch

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

	return nd
}
