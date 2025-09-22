package models

import (
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.org/core/api/tangled"
)

type Repo struct {
	Did         string
	Name        string
	Knot        string
	Rkey        string
	Created     time.Time
	Description string
	Spindle     string
	Labels      []string

	// optionally, populate this when querying for reverse mappings
	RepoStats *RepoStats

	// optional
	Source string
}

func (r *Repo) AsRecord() tangled.Repo {
	var source, spindle, description *string

	if r.Source != "" {
		source = &r.Source
	}

	if r.Spindle != "" {
		spindle = &r.Spindle
	}

	if r.Description != "" {
		description = &r.Description
	}

	return tangled.Repo{
		Knot:        r.Knot,
		Name:        r.Name,
		Description: description,
		CreatedAt:   r.Created.Format(time.RFC3339),
		Source:      source,
		Spindle:     spindle,
		Labels:      r.Labels,
	}
}

func (r Repo) RepoAt() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", r.Did, tangled.RepoNSID, r.Rkey))
}

func (r Repo) DidSlashRepo() string {
	p, _ := securejoin.SecureJoin(r.Did, r.Name)
	return p
}

type RepoStats struct {
	Language   string
	StarCount  int
	IssueCount IssueCount
	PullCount  PullCount
}

type IssueCount struct {
	Open   int
	Closed int
}

type PullCount struct {
	Open    int
	Merged  int
	Closed  int
	Deleted int
}

type RepoLabel struct {
	Id      int64
	RepoAt  syntax.ATURI
	LabelAt syntax.ATURI
}
