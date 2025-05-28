package git

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"tangled.sh/tangled.sh/core/types"
)

var (
	commitCache *ristretto.Cache
	cacheMu     sync.RWMutex
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

var (
	ErrBinaryFile    = fmt.Errorf("binary file")
	ErrNotBinaryFile = fmt.Errorf("not binary file")
)

type GitRepo struct {
	path string
	r    *git.Repository
	h    plumbing.Hash
}

type TagList struct {
	refs []*TagReference
	r    *git.Repository
}

// TagReference is used to list both tag and non-annotated tags.
// Non-annotated tags should only contains a reference.
// Annotated tags should contain its reference and its tag information.
type TagReference struct {
	ref *plumbing.Reference
	tag *object.Tag
}

// infoWrapper wraps the property of a TreeEntry so it can export fs.FileInfo
// to tar WriteHeader
type infoWrapper struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (self *TagList) Len() int {
	return len(self.refs)
}

func (self *TagList) Swap(i, j int) {
	self.refs[i], self.refs[j] = self.refs[j], self.refs[i]
}

// sorting tags in reverse chronological order
func (self *TagList) Less(i, j int) bool {
	var dateI time.Time
	var dateJ time.Time

	if self.refs[i].tag != nil {
		dateI = self.refs[i].tag.Tagger.When
	} else {
		c, err := self.r.CommitObject(self.refs[i].ref.Hash())
		if err != nil {
			dateI = time.Now()
		} else {
			dateI = c.Committer.When
		}
	}

	if self.refs[j].tag != nil {
		dateJ = self.refs[j].tag.Tagger.When
	} else {
		c, err := self.r.CommitObject(self.refs[j].ref.Hash())
		if err != nil {
			dateJ = time.Now()
		} else {
			dateJ = c.Committer.When
		}
	}

	return dateI.After(dateJ)
}

func Open(path string, ref string) (*GitRepo, error) {
	var err error
	g := GitRepo{path: path}
	g.r, err = git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	if ref == "" {
		head, err := g.r.Head()
		if err != nil {
			return nil, fmt.Errorf("getting head of %s: %w", path, err)
		}
		g.h = head.Hash()
	} else {
		hash, err := g.r.ResolveRevision(plumbing.Revision(ref))
		if err != nil {
			return nil, fmt.Errorf("resolving rev %s for %s: %w", ref, path, err)
		}
		g.h = *hash
	}
	return &g, nil
}

func PlainOpen(path string) (*GitRepo, error) {
	var err error
	g := GitRepo{path: path}
	g.r, err = git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return &g, nil
}

func (g *GitRepo) Commits(offset, limit int) ([]*object.Commit, error) {
	commits := []*object.Commit{}

	output, err := g.revList(
		fmt.Sprintf("--skip=%d", offset),
		fmt.Sprintf("--max-count=%d", limit),
	)
	if err != nil {
		return nil, fmt.Errorf("commits from ref: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return commits, nil
	}

	for _, item := range lines {
		obj, err := g.r.CommitObject(plumbing.NewHash(item))
		if err != nil {
			continue
		}
		commits = append(commits, obj)
	}

	return commits, nil
}

func (g *GitRepo) TotalCommits() (int, error) {
	output, err := g.revList(
		fmt.Sprintf("--count"),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to run rev-list", err)
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, err
	}

	return count, nil
}

func (g *GitRepo) revList(extraArgs ...string) ([]byte, error) {
	var args []string
	args = append(args, "rev-list")
	args = append(args, g.h.String())
	args = append(args, extraArgs...)

	cmd := exec.Command("git", args...)
	cmd.Dir = g.path

	return cmd.Output()
}

func (g *GitRepo) Commit(h plumbing.Hash) (*object.Commit, error) {
	return g.r.CommitObject(h)
}

func (g *GitRepo) LastCommit() (*object.Commit, error) {
	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return nil, fmt.Errorf("last commit: %w", err)
	}
	return c, nil
}

func (g *GitRepo) FileContentN(path string, cap int64) ([]byte, error) {
	buf := []byte{}

	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return nil, fmt.Errorf("commit object: %w", err)
	}

	tree, err := c.Tree()
	if err != nil {
		return nil, fmt.Errorf("file tree: %w", err)
	}

	file, err := tree.File(path)
	if err != nil {
		return nil, err
	}

	isbin, _ := file.IsBinary()

	if !isbin {
		reader, err := file.Reader()
		if err != nil {
			return nil, err
		}
		bufReader := io.LimitReader(reader, cap)
		_, err = bufReader.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf, nil
	} else {
		return nil, ErrBinaryFile
	}
}

func (g *GitRepo) FileContent(path string) (string, error) {
	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return "", fmt.Errorf("commit object: %w", err)
	}

	tree, err := c.Tree()
	if err != nil {
		return "", fmt.Errorf("file tree: %w", err)
	}

	file, err := tree.File(path)
	if err != nil {
		return "", err
	}

	isbin, _ := file.IsBinary()

	if !isbin {
		return file.Contents()
	} else {
		return "", ErrBinaryFile
	}
}

func (g *GitRepo) RawContent(path string) ([]byte, error) {
	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return nil, fmt.Errorf("commit object: %w", err)
	}

	tree, err := c.Tree()
	if err != nil {
		return nil, fmt.Errorf("file tree: %w", err)
	}

	file, err := tree.File(path)
	if err != nil {
		return nil, err
	}

	reader, err := file.Reader()
	if err != nil {
		return nil, fmt.Errorf("opening file reader: %w", err)
	}
	defer reader.Close()

	return io.ReadAll(reader)
}

func (g *GitRepo) Tags() ([]*TagReference, error) {
	iter, err := g.r.Tags()
	if err != nil {
		return nil, fmt.Errorf("tag objects: %w", err)
	}

	tags := make([]*TagReference, 0)

	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		obj, err := g.r.TagObject(ref.Hash())
		switch err {
		case nil:
			tags = append(tags, &TagReference{
				ref: ref,
				tag: obj,
			})
		case plumbing.ErrObjectNotFound:
			tags = append(tags, &TagReference{
				ref: ref,
			})
		default:
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	tagList := &TagList{r: g.r, refs: tags}
	sort.Sort(tagList)
	return tags, nil
}

func (g *GitRepo) Branches() ([]types.Branch, error) {
	bi, err := g.r.Branches()
	if err != nil {
		return nil, fmt.Errorf("branchs: %w", err)
	}

	branches := []types.Branch{}

	defaultBranch, err := g.FindMainBranch()

	_ = bi.ForEach(func(ref *plumbing.Reference) error {
		b := types.Branch{}
		b.Hash = ref.Hash().String()
		b.Name = ref.Name().Short()

		// resolve commit that this branch points to
		commit, _ := g.Commit(ref.Hash())
		if commit != nil {
			b.Commit = commit
		}

		if defaultBranch != "" && defaultBranch == b.Name {
			b.IsDefault = true
		}

		branches = append(branches, b)

		return nil
	})

	return branches, nil
}

func (g *GitRepo) Branch(name string) (*plumbing.Reference, error) {
	ref, err := g.r.Reference(plumbing.NewBranchReferenceName(name), false)
	if err != nil {
		return nil, fmt.Errorf("branch: %w", err)
	}

	if !ref.Name().IsBranch() {
		return nil, fmt.Errorf("branch: %s is not a branch", ref.Name())
	}

	return ref, nil
}

func (g *GitRepo) SetDefaultBranch(branch string) error {
	ref := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(branch))
	return g.r.Storer.SetReference(ref)
}

func (g *GitRepo) FindMainBranch() (string, error) {
	ref, err := g.r.Head()
	if err != nil {
		return "", fmt.Errorf("unable to find main branch: %w", err)
	}
	if ref.Name().IsBranch() {
		return strings.TrimPrefix(string(ref.Name()), "refs/heads/"), nil
	}

	return "", fmt.Errorf("unable to find main branch: %w", err)
}

// WriteTar writes itself from a tree into a binary tar file format.
// prefix is root folder to be appended.
func (g *GitRepo) WriteTar(w io.Writer, prefix string) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return fmt.Errorf("commit object: %w", err)
	}

	tree, err := c.Tree()
	if err != nil {
		return err
	}

	walker := object.NewTreeWalker(tree, true, nil)
	defer walker.Close()

	name, entry, err := walker.Next()
	for ; err == nil; name, entry, err = walker.Next() {
		info, err := newInfoWrapper(name, prefix, &entry, tree)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		err = tw.WriteHeader(header)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := tree.File(name)
			if err != nil {
				return err
			}

			reader, err := file.Blob.Reader()
			if err != nil {
				return err
			}

			_, err = io.Copy(tw, reader)
			if err != nil {
				reader.Close()
				return err
			}
			reader.Close()
		}
	}

	return nil
}

func (g *GitRepo) LastCommitForPath(path string) (*types.LastCommitInfo, error) {
	cacheKey := fmt.Sprintf("%s:%s", g.h.String(), path)
	cacheMu.RLock()
	if commitInfo, found := commitCache.Get(cacheKey); found {
		cacheMu.RUnlock()
		return commitInfo.(*types.LastCommitInfo), nil
	}
	cacheMu.RUnlock()

	cmd := exec.Command("git", "-C", g.path, "log", g.h.String(), "-1", "--format=%H %ct", "--", path)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to get commit hash: %w", err)
	}

	output := strings.TrimSpace(out.String())
	if output == "" {
		return nil, fmt.Errorf("no commits found for path: %s", path)
	}

	parts := strings.SplitN(output, " ", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected commit log format")
	}

	commitHash := parts[0]
	commitTimeUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing commit time: %w", err)
	}
	commitTime := time.Unix(commitTimeUnix, 0)

	hash := plumbing.NewHash(commitHash)

	commitInfo := &types.LastCommitInfo{
		Hash:    hash,
		Message: "",
		When:    commitTime,
	}

	cacheMu.Lock()
	commitCache.Set(cacheKey, commitInfo, 1)
	cacheMu.Unlock()

	return commitInfo, nil
}

func newInfoWrapper(
	name string,
	prefix string,
	entry *object.TreeEntry,
	tree *object.Tree,
) (*infoWrapper, error) {
	var (
		size  int64
		mode  fs.FileMode
		isDir bool
	)

	if entry.Mode.IsFile() {
		file, err := tree.TreeEntryFile(entry)
		if err != nil {
			return nil, err
		}
		mode = fs.FileMode(file.Mode)

		size, err = tree.Size(name)
		if err != nil {
			return nil, err
		}
	} else {
		isDir = true
		mode = fs.ModeDir | fs.ModePerm
	}

	fullname := path.Join(prefix, name)
	return &infoWrapper{
		name:    fullname,
		size:    size,
		mode:    mode,
		modTime: time.Unix(0, 0),
		isDir:   isDir,
	}, nil
}

func (i *infoWrapper) Name() string {
	return i.name
}

func (i *infoWrapper) Size() int64 {
	return i.size
}

func (i *infoWrapper) Mode() fs.FileMode {
	return i.mode
}

func (i *infoWrapper) ModTime() time.Time {
	return i.modTime
}

func (i *infoWrapper) IsDir() bool {
	return i.isDir
}

func (i *infoWrapper) Sys() any {
	return nil
}

func (t *TagReference) Name() string {
	return t.ref.Name().Short()
}

func (t *TagReference) Message() string {
	if t.tag != nil {
		return t.tag.Message
	}
	return ""
}

func (t *TagReference) TagObject() *object.Tag {
	return t.tag
}

func (t *TagReference) Hash() plumbing.Hash {
	return t.ref.Hash()
}
