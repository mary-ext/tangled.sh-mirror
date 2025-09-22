package models

import (
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Collaborator struct {
	// identifiers for the record
	Id   int64
	Did  syntax.DID
	Rkey string

	// content
	SubjectDid syntax.DID
	RepoAt     syntax.ATURI

	// meta
	Created time.Time
}
