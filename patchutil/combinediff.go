package patchutil

import (
	"fmt"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// original1 -> patch1 -> rev1
// original2 -> patch2 -> rev2
//
// original2 must be equal to rev1, so we can merge them to get maximal context
//
// finally,
// rev2' <- apply(patch2, merged)
// combineddiff <- diff(rev2', original1)
func combineFiles(file1, file2 *gitdiff.File) (*gitdiff.File, error) {
	fileName := bestName(file1)

	o1 := CreatePreImage(file1)
	r1 := CreatePostImage(file1)
	o2 := CreatePreImage(file2)

	merged, err := r1.Merge(&o2)
	if err != nil {
		return nil, err
	}

	r2Prime, err := merged.Apply(file2)
	if err != nil {
		return nil, err
	}

	// produce combined diff
	diff, err := Unified(o1.String(), fileName, r2Prime, fileName)
	if err != nil {
		return nil, err
	}

	parsed, _, err := gitdiff.Parse(strings.NewReader(diff))

	if len(parsed) != 1 {
		// no diff? the second commit reverted the changes from the first
		return nil, nil
	}

	return parsed[0], nil
}

// use empty lines for lines we are unaware of
//
// this raises an error only if the two patches were invalid or non-contiguous
func mergeLines(old, new string) (string, error) {
	var i, j int

	// TODO: use strings.Lines
	linesOld := strings.Split(old, "\n")
	linesNew := strings.Split(new, "\n")

	result := []string{}

	for i < len(linesOld) || j < len(linesNew) {
		if i >= len(linesOld) {
			// rest of the file is populated from `new`
			result = append(result, linesNew[j])
			j++
			continue
		}

		if j >= len(linesNew) {
			// rest of the file is populated from `old`
			result = append(result, linesOld[i])
			i++
			continue
		}

		oldLine := linesOld[i]
		newLine := linesNew[j]

		if oldLine != newLine && (oldLine != "" && newLine != "") {
			// context mismatch
			return "", fmt.Errorf("failed to merge files, found context mismatch at %d; oldLine: `%s`, newline: `%s`", i+1, oldLine, newLine)
		}

		if oldLine == newLine {
			result = append(result, oldLine)
		} else if oldLine == "" {
			result = append(result, newLine)
		} else if newLine == "" {
			result = append(result, oldLine)
		}
		i++
		j++
	}

	return strings.Join(result, "\n"), nil
}

func combineTwo(patch1, patch2 []*gitdiff.File) []*gitdiff.File {
	fileToIdx1 := make(map[string]int)
	fileToIdx2 := make(map[string]int)
	visited := make(map[string]struct{})
	var result []*gitdiff.File

	for idx, f := range patch1 {
		fileToIdx1[bestName(f)] = idx
	}

	for idx, f := range patch2 {
		fileToIdx2[bestName(f)] = idx
	}

	for _, f1 := range patch1 {
		fileName := bestName(f1)
		if idx, ok := fileToIdx2[fileName]; ok {
			f2 := patch2[idx]

			// we have f1 and f2, combine them
			combined, err := combineFiles(f1, f2)
			if err != nil {
				fmt.Println(err)
			}

			// combined can be nil commit 2 reverted all changes from commit 1
			if combined != nil {
				result = append(result, combined)
			}

		} else {
			// only in patch1; add as-is
			result = append(result, f1)
		}

		visited[fileName] = struct{}{}
	}

	// for all files in patch2 that remain unvisited; we can just add them into the output
	for _, f2 := range patch2 {
		fileName := bestName(f2)
		if _, ok := visited[fileName]; ok {
			continue
		}

		result = append(result, f2)
	}

	return result
}

// pairwise combination from first to last patch
func CombineDiff(patches ...[]*gitdiff.File) []*gitdiff.File {
	if len(patches) == 0 {
		return nil
	}

	if len(patches) == 1 {
		return patches[0]
	}

	combined := combineTwo(patches[0], patches[1])

	newPatches := [][]*gitdiff.File{}
	newPatches = append(newPatches, combined)
	for i, p := range patches {
		if i >= 2 {
			newPatches = append(newPatches, p)
		}
	}

	return CombineDiff(newPatches...)
}
