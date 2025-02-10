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
