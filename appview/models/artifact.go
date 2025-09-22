package models

import (
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ipfs/go-cid"
	"tangled.org/core/api/tangled"
)

type Artifact struct {
	Id   uint64
	Did  string
	Rkey string

	RepoAt    syntax.ATURI
	Tag       plumbing.Hash
	CreatedAt time.Time

	BlobCid  cid.Cid
	Name     string
	Size     uint64
	MimeType string
}

func (a *Artifact) ArtifactAt() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", a.Did, tangled.RepoArtifactNSID, a.Rkey))
}
