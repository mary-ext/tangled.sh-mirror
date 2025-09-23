package models

import (
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Star struct {
	StarredByDid string
	RepoAt       syntax.ATURI
	Created      time.Time
	Rkey         string

	// optionally, populate this when querying for reverse mappings
	Repo *Repo
}
