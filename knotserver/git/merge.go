package git

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/dgraph-io/ristretto"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"tangled.org/core/patchutil"
	"tangled.org/core/types"
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

func (m *MergeCheckCache) cacheKey(g *GitRepo, patch string, targetBranch string) string {
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

func (m *MergeCheckCache) Set(g *GitRepo, patch string, targetBranch string, mergeCheck error) {
	key := m.cacheKey(g, patch, targetBranch)
	val := m.cacheVal(mergeCheck)
	m.cache.Set(key, val, 0)
}

func (m *MergeCheckCache) Get(g *GitRepo, patch string, targetBranch string) (error, bool) {
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
	CommitMessage  string
	CommitBody     string
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	FormatPatch    bool
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

func (g *GitRepo) createTempFileWithPatch(patchData string) (string, error) {
	tmpFile, err := os.CreateTemp("", "git-patch-*.patch")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary patch file: %w", err)
	}

	if _, err := tmpFile.Write([]byte(patchData)); err != nil {
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

func (g *GitRepo) checkPatch(tmpDir, patchFile string) error {
	var stderr bytes.Buffer

	cmd := exec.Command("git", "-C", tmpDir, "apply", "--check", "-v", patchFile)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		conflicts := parseGitApplyErrors(stderr.String())
		return &ErrMerge{
			Message:     "patch cannot be applied cleanly",
			Conflicts:   conflicts,
			HasConflict: len(conflicts) > 0,
			OtherError:  err,
		}
	}
	return nil
}

func (g *GitRepo) applyPatch(patchData, patchFile string, opts MergeOptions) error {
	var stderr bytes.Buffer
	var cmd *exec.Cmd

	// configure default git user before merge
	exec.Command("git", "-C", g.path, "config", "user.name", opts.CommitterName).Run()
	exec.Command("git", "-C", g.path, "config", "user.email", opts.CommitterEmail).Run()
	exec.Command("git", "-C", g.path, "config", "advice.mergeConflict", "false").Run()

	// if patch is a format-patch, apply using 'git am'
	if opts.FormatPatch {
		return g.applyMailbox(patchData)
	}

	// else, apply using 'git apply' and commit it manually
	applyCmd := exec.Command("git", "-C", g.path, "apply", patchFile)
	applyCmd.Stderr = &stderr
	if err := applyCmd.Run(); err != nil {
		return fmt.Errorf("patch application failed: %s", stderr.String())
	}

	stageCmd := exec.Command("git", "-C", g.path, "add", ".")
	if err := stageCmd.Run(); err != nil {
		return fmt.Errorf("failed to stage changes: %w", err)
	}

	commitArgs := []string{"-C", g.path, "commit"}

	// Set author if provided
	authorName := opts.AuthorName
	authorEmail := opts.AuthorEmail

	if authorName != "" && authorEmail != "" {
		commitArgs = append(commitArgs, "--author", fmt.Sprintf("%s <%s>", authorName, authorEmail))
	}
	// else, will default to knot's global user.name & user.email configured via `KNOT_GIT_USER_*` env variables

	commitArgs = append(commitArgs, "-m", opts.CommitMessage)

	if opts.CommitBody != "" {
		commitArgs = append(commitArgs, "-m", opts.CommitBody)
	}

	cmd = exec.Command("git", commitArgs...)

	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("patch application failed: %s", stderr.String())
	}

	return nil
}

func (g *GitRepo) applyMailbox(patchData string) error {
	fps, err := patchutil.ExtractPatches(patchData)
	if err != nil {
		return fmt.Errorf("failed to extract patches: %w", err)
	}

	// apply each patch one by one
	// update the newly created commit object to add the change-id header
	total := len(fps)
	for i, p := range fps {
		newCommit, err := g.applySingleMailbox(p)
		if err != nil {
			return err
		}

		log.Printf("applying mailbox patch %d/%d: committed %s\n", i+1, total, newCommit.String())
	}

	return nil
}

func (g *GitRepo) applySingleMailbox(singlePatch types.FormatPatch) (plumbing.Hash, error) {
	tmpPatch, err := g.createTempFileWithPatch(singlePatch.Raw)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to create temporary patch file for singluar mailbox patch: %w", err)
	}

	var stderr bytes.Buffer
	cmd := exec.Command("git", "-C", g.path, "am", tmpPatch)
	cmd.Stderr = &stderr

	head, err := g.r.Head()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	log.Println("head before apply", head.Hash().String())

	if err := cmd.Run(); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("patch application failed: %s", stderr.String())
	}

	if err := g.Refresh(); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to refresh repository state: %w", err)
	}

	head, err = g.r.Head()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	log.Println("head after apply", head.Hash().String())

	newHash := head.Hash()
	if changeId, err := singlePatch.ChangeId(); err != nil {
		// no change ID
	} else if updatedHash, err := g.setChangeId(head.Hash(), changeId); err != nil {
		return plumbing.ZeroHash, err
	} else {
		newHash = updatedHash
	}

	return newHash, nil
}

func (g *GitRepo) setChangeId(hash plumbing.Hash, changeId string) (plumbing.Hash, error) {
	log.Printf("updating change ID of %s to %s\n", hash.String(), changeId)
	obj, err := g.r.CommitObject(hash)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to get commit object for hash %s: %w", hash.String(), err)
	}

	// write the change-id header
	obj.ExtraHeaders["change-id"] = []byte(changeId)

	// create a new object
	dest := g.r.Storer.NewEncodedObject()
	if err := obj.Encode(dest); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to create new object: %w", err)
	}

	// store the new object
	newHash, err := g.r.Storer.SetEncodedObject(dest)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to store new object: %w", err)
	}

	log.Printf("hash changed from %s to %s\n", obj.Hash.String(), newHash.String())

	// find the branch that HEAD is pointing to
	ref, err := g.r.Head()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to fetch HEAD: %w", err)
	}

	// and update that branch to point to new commit
	if ref.Name().IsBranch() {
		err = g.r.Storer.SetReference(plumbing.NewHashReference(ref.Name(), newHash))
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf("failed to update HEAD: %w", err)
		}
	}

	// new hash of commit
	return newHash, nil
}

func (g *GitRepo) MergeCheck(patchData string, targetBranch string) error {
	if val, ok := mergeCheckCache.Get(g, patchData, targetBranch); ok {
		return val
	}

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

	result := g.checkPatch(tmpDir, patchFile)
	mergeCheckCache.Set(g, patchData, targetBranch, result)
	return result
}

func (g *GitRepo) MergeWithOptions(patchData string, targetBranch string, opts MergeOptions) error {
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

	tmpRepo, err := PlainOpen(tmpDir)
	if err != nil {
		return err
	}

	if err := tmpRepo.applyPatch(patchData, patchFile, opts); err != nil {
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
