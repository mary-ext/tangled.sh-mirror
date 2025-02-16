package types

import (
	"html/template"

	"github.com/go-git/go-git/v5/plumbing/object"
)

type RepoIndexResponse struct {
	IsEmpty     bool             `json:"is_empty"`
	Ref         string           `json:"ref,omitempty"`
	Readme      template.HTML    `json:"readme,omitempty"`
	Commits     []*object.Commit `json:"commits,omitempty"`
	Description string           `json:"description,omitempty"`
	Files       []NiceTree       `json:"files,omitempty"`
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

type RepoTreeResponse struct {
	Ref         string     `json:"ref,omitempty"`
	Parent      string     `json:"parent,omitempty"`
	Description string     `json:"description,omitempty"`
	DotDot      string     `json:"dotdot,omitempty"`
	Files       []NiceTree `json:"files,omitempty"`
}

type TagReference struct {
	Ref     Reference   `json:"ref,omitempty"`
	Tag     *object.Tag `json:"tag,omitempty"`
	Message string      `json:"message,omitempty"`
}

type Reference struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type Branch struct {
	Reference `json:"reference"`
}

type RepoTagsResponse struct {
	Tags []*TagReference `json:"tags,omitempty"`
}

type RepoBranchesResponse struct {
	Branches []Branch `json:"branches,omitempty"`
}

type RepoBlobResponse struct {
	Contents string `json:"contents,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Path     string `json:"path,omitempty"`
	IsBinary bool   `json:"is_binary,omitempty"`

	Lines int `json:"lines,omitempty"`
}
