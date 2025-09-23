package models

import (
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Spindle struct {
	Id           int
	Owner        syntax.DID
	Instance     string
	Verified     *time.Time
	Created      time.Time
	NeedsUpgrade bool
}

type SpindleMember struct {
	Id       int
	Did      syntax.DID // owner of the record
	Rkey     string     // rkey of the record
	Instance string
	Subject  syntax.DID // the member being added
	Created  time.Time
}
