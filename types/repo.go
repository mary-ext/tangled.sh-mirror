package types

import (
	"github.com/go-git/go-git/v5/plumbing/object"
)

type RepoIndexResponse struct {
	IsEmpty        bool             `json:"is_empty"`
	Ref            string           `json:"ref,omitempty"`
	Readme         string           `json:"readme,omitempty"`
	ReadmeFileName string           `json:"readme_file_name,omitempty"`
	Commits        []*object.Commit `json:"commits,omitempty"`
	Description    string           `json:"description,omitempty"`
	Files          []NiceTree       `json:"files,omitempty"`
	Branches       []Branch         `json:"branches,omitempty"`
	Tags           []*TagReference  `json:"tags,omitempty"`
	TotalCommits   int              `json:"total_commits,omitempty"`
}

type RepoLogResponse struct {
	Commits     []*object.Commit `json:"commits,omitempty"`
	Ref         string           `json:"ref,omitempty"`
	Description string           `json:"description,omitempty"`
	Log         bool             `json:"log,omitempty"`
	Total       int              `json:"total,omitempty"`
	Page        int              `json:"page,omitempty"`
	PerPage     int              `json:"per_page,omitempty"`
}

type RepoCommitResponse struct {
	Ref  string    `json:"ref,omitempty"`
	Diff *NiceDiff `json:"diff,omitempty"`
}

type RepoFormatPatchResponse struct {
	Rev1        string        `json:"rev1,omitempty"`
	Rev2        string        `json:"rev2,omitempty"`
	FormatPatch []FormatPatch `json:"format_patch,omitempty"`
	MergeBase   string        `json:"merge_base,omitempty"` // deprecated
	Patch       string        `json:"patch,omitempty"`
}

type RepoTreeResponse struct {
	Ref            string     `json:"ref,omitempty"`
	Parent         string     `json:"parent,omitempty"`
	Description    string     `json:"description,omitempty"`
	DotDot         string     `json:"dotdot,omitempty"`
	Files          []NiceTree `json:"files,omitempty"`
	ReadmeFileName string     `json:"readme_filename,omitempty"`
	Readme         string     `json:"readme_contents,omitempty"`
}

type TagReference struct {
	Reference
	Tag     *object.Tag `json:"tag,omitempty"`
	Message string      `json:"message,omitempty"`
}

type Reference struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type Branch struct {
	Reference `json:"reference"`
	Commit    *object.Commit `json:"commit,omitempty"`
	IsDefault bool           `json:"is_deafult,omitempty"`
}

type RepoTagsResponse struct {
	Tags []*TagReference `json:"tags,omitempty"`
}

type RepoBranchesResponse struct {
	Branches []Branch `json:"branches,omitempty"`
}

type RepoBranchResponse struct {
	Branch Branch
}

type RepoDefaultBranchResponse struct {
	Branch string `json:"branch,omitempty"`
}

type RepoBlobResponse struct {
	Contents string `json:"contents,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Path     string `json:"path,omitempty"`
	IsBinary bool   `json:"is_binary,omitempty"`

	Lines    int    `json:"lines,omitempty"`
	SizeHint uint64 `json:"size_hint,omitempty"`
}

type ForkStatus int

const (
	UpToDate        ForkStatus = 0
	FastForwardable            = 1
	Conflict                   = 2
	MissingBranch              = 3
)

type ForkInfo struct {
	IsFork bool
	Status ForkStatus
}

type AncestorCheckResponse struct {
	Status ForkStatus `json:"status"`
}

type RepoLanguageDetails struct {
	Name       string
	Percentage float32
	Color      string
}

type RepoLanguageResponse struct {
	// Language: File count
	Languages map[string]int64 `json:"languages"`
}
