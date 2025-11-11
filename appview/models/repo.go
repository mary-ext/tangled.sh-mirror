package models

import (
	"fmt"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.org/core/api/tangled"
)

type Repo struct {
	Id          int64
	Did         string
	Name        string
	Knot        string
	Rkey        string
	Created     time.Time
	Description string
	Website     string
	Topics      []string
	Spindle     string
	Labels      []string

	// optionally, populate this when querying for reverse mappings
	RepoStats *RepoStats

	// optional
	Source string
}

func (r *Repo) AsRecord() tangled.Repo {
	var source, spindle, description, website *string

	if r.Source != "" {
		source = &r.Source
	}

	if r.Spindle != "" {
		spindle = &r.Spindle
	}

	if r.Description != "" {
		description = &r.Description
	}

	if r.Website != "" {
		website = &r.Website
	}

	return tangled.Repo{
		Knot:        r.Knot,
		Name:        r.Name,
		Description: description,
		Website:     website,
		Topics:      r.Topics,
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

func (r Repo) TopicStr() string {
	return strings.Join(r.Topics, " ")
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

type RepoGroup struct {
	Repo   *Repo
	Issues []Issue
}

type BlobContentType int

const (
	BlobContentTypeCode BlobContentType = iota
	BlobContentTypeMarkup
	BlobContentTypeImage
	BlobContentTypeSvg
	BlobContentTypeVideo
	BlobContentTypeSubmodule
)

func (ty BlobContentType) IsCode() bool      { return ty == BlobContentTypeCode }
func (ty BlobContentType) IsMarkup() bool    { return ty == BlobContentTypeMarkup }
func (ty BlobContentType) IsImage() bool     { return ty == BlobContentTypeImage }
func (ty BlobContentType) IsSvg() bool       { return ty == BlobContentTypeSvg }
func (ty BlobContentType) IsVideo() bool     { return ty == BlobContentTypeVideo }
func (ty BlobContentType) IsSubmodule() bool { return ty == BlobContentTypeSubmodule }

type BlobView struct {
	HasTextView     bool // can show as code/text
	HasRenderedView bool // can show rendered (markup/image/video/submodule)
	HasRawView      bool // can download raw (everything except submodule)

	// current display mode
	ShowingRendered bool // currently in rendered mode
	ShowingText     bool // currently in text/code mode

	// content type flags
	ContentType BlobContentType

	// Content data
	Contents   string
	ContentSrc string // URL for media files
	Lines      int
	SizeHint   uint64
}

// if both views are available, then show a toggle between them
func (b BlobView) ShowToggle() bool {
	return b.HasTextView && b.HasRenderedView
}

func (b BlobView) IsUnsupported() bool {
	// no view available, only raw
	return !(b.HasRenderedView || b.HasTextView)
}
