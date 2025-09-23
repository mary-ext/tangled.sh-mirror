package models

import (
	"github.com/bluesky-social/indigo/atproto/syntax"
)

type RepoLanguage struct {
	Id           int64
	RepoAt       syntax.ATURI
	Ref          string
	IsDefaultRef bool
	Language     string
	Bytes        int64
}
