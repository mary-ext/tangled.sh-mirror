package git

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/dgraph-io/ristretto"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"tangled.sh/tangled.sh/core/patchutil"
)

type MergeCheckCache struct {
	cache *ristretto.Cache
}

var (
	mergeCheckCache MergeCheckCache
)

func init() {
	cache, _ := ristretto.NewCache(&ristretto.Config{
		NumCounters:            1e7,
		MaxCost:                1 << 30,
		BufferItems:            64,
		TtlTickerDurationInSec: 60 * 60 * 24 * 2, // 2 days
	})
	mergeCheckCache = MergeCheckCache{cache}
}

func (m *MergeCheckCache) cacheKey(g *GitRepo, patch []byte, targetBranch string) string {
	sep := byte(':')
	hash := sha256.Sum256(fmt.Append([]byte{}, g.path, sep, g.h.String(), sep, patch, sep, targetBranch))
	return fmt.Sprintf("%x", hash)
}

// we can't cache "mergeable" in risetto, nil is not cacheable
//
// we use the sentinel value instead
func (m *MergeCheckCache) cacheVal(check error) any {
	if check == nil {
		return struct{}{}
	} else {
		return check
	}
}

func (m *MergeCheckCache) Set(g *GitRepo, patch []byte, targetBranch string, mergeCheck error) {
	key := m.cacheKey(g, patch, targetBranch)
	val := m.cacheVal(mergeCheck)
	m.cache.Set(key, val, 0)
}

func (m *MergeCheckCache) Get(g *GitRepo, patch []byte, targetBranch string) (error, bool) {
	key := m.cacheKey(g, patch, targetBranch)
	if val, ok := m.cache.Get(key); ok {
		if val == struct{}{} {
			// cache hit for mergeable
			return nil, true
		} else if e, ok := val.(error); ok {
			// cache hit for merge conflict
			return e, true
		}
	}

	// cache miss
	return nil, false
}

type ErrMerge struct {
	Message     string
	Conflicts   []ConflictInfo
	HasConflict bool
	OtherError  error
}

type ConflictInfo struct {
	Filename string
	Reason   string
}

// MergeOptions specifies the configuration for a merge operation
type MergeOptions struct {
	CommitMessage string
	CommitBody    string
	AuthorName    string
	AuthorEmail   string
	FormatPatch   bool
}

func (e ErrMerge) Error() string {
	if e.HasConflict {
		return fmt.Sprintf("merge failed due to conflicts: %s (%d conflicts)", e.Message, len(e.Conflicts))
	}
	if e.OtherError != nil {
		return fmt.Sprintf("merge failed: %s: %v", e.Message, e.OtherError)
	}
	return fmt.Sprintf("merge failed: %s", e.Message)
}

func (g *GitRepo) createTempFileWithPatch(patchData []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "git-patch-*.patch")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary patch file: %w", err)
	}

	if _, err := tmpFile.Write(patchData); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to write patch data to temporary file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to close temporary patch file: %w", err)
	}

	return tmpFile.Name(), nil
}

func (g *GitRepo) cloneRepository(targetBranch string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "git-clone-")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}

	_, err = git.PlainClone(tmpDir, false, &git.CloneOptions{
		URL:           "file://" + g.path,
		Depth:         1,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(targetBranch),
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to clone repository: %w", err)
	}

	return tmpDir, nil
}

func (g *GitRepo) applyPatch(tmpDir, patchFile string, checkOnly bool, opts *MergeOptions) error {
	var stderr bytes.Buffer
	var cmd *exec.Cmd

	if checkOnly {
		cmd = exec.Command("git", "-C", tmpDir, "apply", "--check", "-v", patchFile)
	} else {
		// if patch is a format-patch, apply using 'git am'
		if opts.FormatPatch {
			amCmd := exec.Command("git", "-C", tmpDir, "am", patchFile)
			amCmd.Stderr = &stderr
			if err := amCmd.Run(); err != nil {
				return fmt.Errorf("patch application failed: %s", stderr.String())
			}
			return nil
		}

		// else, apply using 'git apply' and commit it manually
		exec.Command("git", "-C", tmpDir, "config", "advice.mergeConflict", "false").Run()
		if opts != nil {
			applyCmd := exec.Command("git", "-C", tmpDir, "apply", patchFile)
			applyCmd.Stderr = &stderr
			if err := applyCmd.Run(); err != nil {
				return fmt.Errorf("patch application failed: %s", stderr.String())
			}

			stageCmd := exec.Command("git", "-C", tmpDir, "add", ".")
			if err := stageCmd.Run(); err != nil {
				return fmt.Errorf("failed to stage changes: %w", err)
			}

			commitArgs := []string{"-C", tmpDir, "commit"}

			// Set author if provided
			authorName := opts.AuthorName
			authorEmail := opts.AuthorEmail

			if authorEmail == "" {
				authorEmail = "noreply@tangled.sh"
			}

			if authorName == "" {
				authorName = "Tangled"
			}

			if authorName != "" {
				commitArgs = append(commitArgs, "--author", fmt.Sprintf("%s <%s>", authorName, authorEmail))
			}

			commitArgs = append(commitArgs, "-m", opts.CommitMessage)

			if opts.CommitBody != "" {
				commitArgs = append(commitArgs, "-m", opts.CommitBody)
			}

			cmd = exec.Command("git", commitArgs...)
		} else {
			// If no commit message specified, use git-am which automatically creates a commit
			cmd = exec.Command("git", "-C", tmpDir, "am", patchFile)
		}
	}

	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if checkOnly {
			conflicts := parseGitApplyErrors(stderr.String())
			return &ErrMerge{
				Message:     "patch cannot be applied cleanly",
				Conflicts:   conflicts,
				HasConflict: len(conflicts) > 0,
				OtherError:  err,
			}
		}
		return fmt.Errorf("patch application failed: %s", stderr.String())
	}

	return nil
}

func (g *GitRepo) MergeCheck(patchData []byte, targetBranch string) error {
	if val, ok := mergeCheckCache.Get(g, patchData, targetBranch); ok {
		return val
	}

	var opts MergeOptions
	opts.FormatPatch = patchutil.IsFormatPatch(string(patchData))

	patchFile, err := g.createTempFileWithPatch(patchData)
	if err != nil {
		return &ErrMerge{
			Message:    err.Error(),
			OtherError: err,
		}
	}
	defer os.Remove(patchFile)

	tmpDir, err := g.cloneRepository(targetBranch)
	if err != nil {
		return &ErrMerge{
			Message:    err.Error(),
			OtherError: err,
		}
	}
	defer os.RemoveAll(tmpDir)

	result := g.applyPatch(tmpDir, patchFile, true, &opts)
	mergeCheckCache.Set(g, patchData, targetBranch, result)
	return result
}

func (g *GitRepo) Merge(patchData []byte, targetBranch string) error {
	return g.MergeWithOptions(patchData, targetBranch, nil)
}

func (g *GitRepo) MergeWithOptions(patchData []byte, targetBranch string, opts *MergeOptions) error {
	patchFile, err := g.createTempFileWithPatch(patchData)
	if err != nil {
		return &ErrMerge{
			Message:    err.Error(),
			OtherError: err,
		}
	}
	defer os.Remove(patchFile)

	tmpDir, err := g.cloneRepository(targetBranch)
	if err != nil {
		return &ErrMerge{
			Message:    err.Error(),
			OtherError: err,
		}
	}
	defer os.RemoveAll(tmpDir)

	if err := g.applyPatch(tmpDir, patchFile, false, opts); err != nil {
		return err
	}

	pushCmd := exec.Command("git", "-C", tmpDir, "push")
	if err := pushCmd.Run(); err != nil {
		return &ErrMerge{
			Message:    "failed to push changes to bare repository",
			OtherError: err,
		}
	}

	return nil
}

func parseGitApplyErrors(errorOutput string) []ConflictInfo {
	var conflicts []ConflictInfo
	lines := strings.Split(errorOutput, "\n")

	var currentFile string

	for i := range lines {
		line := strings.TrimSpace(lines[i])

		if strings.HasPrefix(line, "error: patch failed:") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) >= 3 {
				currentFile = strings.TrimSpace(parts[2])
			}
			continue
		}

		if match := regexp.MustCompile(`^error: (.*):(\d+): (.*)$`).FindStringSubmatch(line); len(match) >= 4 {
			if currentFile == "" {
				currentFile = match[1]
			}

			conflicts = append(conflicts, ConflictInfo{
				Filename: currentFile,
				Reason:   match[3],
			})
			continue
		}

		if strings.Contains(line, "already exists in working directory") {
			conflicts = append(conflicts, ConflictInfo{
				Filename: currentFile,
				Reason:   "file already exists",
			})
		} else if strings.Contains(line, "does not exist in working tree") {
			conflicts = append(conflicts, ConflictInfo{
				Filename: currentFile,
				Reason:   "file does not exist",
			})
		} else if strings.Contains(line, "patch does not apply") {
			conflicts = append(conflicts, ConflictInfo{
				Filename: currentFile,
				Reason:   "patch does not apply",
			})
		}
	}

	return conflicts
}
