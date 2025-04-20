package patchutil

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type FormatPatch struct {
	Files []*gitdiff.File
	*gitdiff.PatchHeader
}

func ExtractPatches(formatPatch string) ([]FormatPatch, error) {
	patches := splitFormatPatch(formatPatch)

	result := []FormatPatch{}

	for _, patch := range patches {
		files, headerStr, err := gitdiff.Parse(strings.NewReader(patch))
		if err != nil {
			return nil, fmt.Errorf("failed to parse patch: %w", err)
		}

		header, err := gitdiff.ParsePatchHeader(headerStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse patch header: %w", err)
		}

		result = append(result, FormatPatch{
			Files:       files,
			PatchHeader: header,
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
