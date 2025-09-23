package db

import (
	"fmt"
	"strings"
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

func AddArtifact(e Execer, artifact Artifact) error {
	_, err := e.Exec(
		`insert or ignore into artifacts (
			did,
			rkey,
			repo_at,
			tag,
			created,
			blob_cid,
			name,
			size,
			mimetype
		)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.Did,
		artifact.Rkey,
		artifact.RepoAt,
		artifact.Tag[:],
		artifact.CreatedAt.Format(time.RFC3339),
		artifact.BlobCid.String(),
		artifact.Name,
		artifact.Size,
		artifact.MimeType,
	)
	return err
}

func GetArtifact(e Execer, filters ...filter) ([]Artifact, error) {
	var artifacts []Artifact

	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(`select
			did,
			rkey,
			repo_at,
			tag,
			created,
			blob_cid,
			name,
			size,
			mimetype
		from artifacts %s`,
		whereClause,
	)

	rows, err := e.Query(query, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var artifact Artifact
		var createdAt string
		var tag []byte
		var blobCid string

		if err := rows.Scan(
			&artifact.Did,
			&artifact.Rkey,
			&artifact.RepoAt,
			&tag,
			&createdAt,
			&blobCid,
			&artifact.Name,
			&artifact.Size,
			&artifact.MimeType,
		); err != nil {
			return nil, err
		}

		artifact.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			artifact.CreatedAt = time.Now()
		}
		artifact.Tag = plumbing.Hash(tag)
		artifact.BlobCid = cid.MustParse(blobCid)

		artifacts = append(artifacts, artifact)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return artifacts, nil
}

func DeleteArtifact(e Execer, filters ...filter) error {
	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(`delete from artifacts %s`, whereClause)

	_, err := e.Exec(query, args...)
	return err
}
