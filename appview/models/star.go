package models

import (
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Star struct {
	Did     string
	RepoAt  syntax.ATURI
	Created time.Time
	Rkey    string
}

// RepoStar is used for reverse mapping to repos
type RepoStar struct {
	Star
	Repo *Repo
}

// StringStar is used for reverse mapping to strings
type StringStar struct {
	Star
	String *String
}
