package git

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

var (
	commitCache *ristretto.Cache
)

func init() {
	cache, _ := ristretto.NewCache(&ristretto.Config{
		NumCounters:            1e7,
		MaxCost:                1 << 30,
		BufferItems:            64,
		TtlTickerDurationInSec: 120,
	})
	commitCache = cache
}

// processReader wraps a reader and ensures the associated process is cleaned up
type processReader struct {
	io.Reader
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

func (pr *processReader) Close() error {
	if err := pr.stdout.Close(); err != nil {
		return err
	}
	return pr.cmd.Wait()
}

func (g *GitRepo) streamingGitLog(ctx context.Context, extraArgs ...string) (io.ReadCloser, error) {
	args := []string{}
	args = append(args, "log")
	args = append(args, g.h.String())
	args = append(args, extraArgs...)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.path

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &processReader{
		Reader: stdout,
		cmd:    cmd,
		stdout: stdout,
	}, nil
}

type commit struct {
	hash    plumbing.Hash
	when    time.Time
	files   []string
	message string
}

func cacheKey(g *GitRepo, path string) string {
	sep := byte(':')
	hash := sha256.Sum256(fmt.Append([]byte{}, g.path, sep, g.h.String(), sep, path))
	return fmt.Sprintf("%x", hash)
}

func (g *GitRepo) calculateCommitTimeIn(ctx context.Context, subtree *object.Tree, parent string, timeout time.Duration) (map[string]commit, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return g.calculateCommitTime(ctx, subtree, parent)
}

func (g *GitRepo) calculateCommitTime(ctx context.Context, subtree *object.Tree, parent string) (map[string]commit, error) {
	filesToDo := make(map[string]struct{})
	filesDone := make(map[string]commit)
	for _, e := range subtree.Entries {
		fpath := path.Clean(path.Join(parent, e.Name))
		filesToDo[fpath] = struct{}{}
	}

	for _, e := range subtree.Entries {
		f := path.Clean(path.Join(parent, e.Name))
		cacheKey := cacheKey(g, f)
		if cached, ok := commitCache.Get(cacheKey); ok {
			filesDone[f] = cached.(commit)
			delete(filesToDo, f)
		} else {
			filesToDo[f] = struct{}{}
		}
	}

	if len(filesToDo) == 0 {
		return filesDone, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pathSpec := "."
	if parent != "" {
		pathSpec = parent
	}
	output, err := g.streamingGitLog(ctx, "--pretty=format:%H,%ad,%s", "--date=iso", "--name-only", "--", pathSpec)
	if err != nil {
		return nil, err
	}
	defer output.Close() // Ensure the git process is properly cleaned up

	reader := bufio.NewReader(output)
	var current commit
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		line = strings.TrimSpace(line)

		if line == "" {
			if !current.hash.IsZero() {
				// we have a fully parsed commit
				for _, f := range current.files {
					if _, ok := filesToDo[f]; ok {
						filesDone[f] = current
						delete(filesToDo, f)
						commitCache.Set(cacheKey(g, f), current, 0)
					}
				}

				if len(filesToDo) == 0 {
					cancel()
					break
				}
				current = commit{}
			}
		} else if current.hash.IsZero() {
			parts := strings.SplitN(line, ",", 3)
			if len(parts) == 3 {
				current.hash = plumbing.NewHash(parts[0])
				current.when, _ = time.Parse("2006-01-02 15:04:05 -0700", parts[1])
				current.message = parts[2]
			}
		} else {
			// all ancestors along this path should also be included
			file := path.Clean(line)
			ancestors := ancestors(file)
			current.files = append(current.files, file)
			current.files = append(current.files, ancestors...)
		}

		if err == io.EOF {
			break
		}
	}

	return filesDone, nil
}

func ancestors(p string) []string {
	var ancestors []string

	for {
		p = path.Dir(p)
		if p == "." || p == "/" {
			break
		}
		ancestors = append(ancestors, p)
	}
	return ancestors
}
