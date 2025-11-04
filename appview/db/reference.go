package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/models"
)

// FindReferences resolves refLinks to Issue/PR/IssueComment/PullComment ATURIs.
// It will ignore missing refLinks.
func FindReferences(e Execer, refLinks []models.ReferenceLink) ([]syntax.ATURI, error) {
	var (
		issueRefs []models.ReferenceLink
		pullRefs  []models.ReferenceLink
	)
	for _, ref := range refLinks {
		switch ref.Kind {
		case models.RefKindIssue:
			issueRefs = append(issueRefs, ref)
		case models.RefKindPull:
			pullRefs = append(pullRefs, ref)
		}
	}
	issueUris, err := findIssueReferences(e, issueRefs)
	if err != nil {
		return nil, err
	}
	pullUris, err := findPullReferences(e, pullRefs)
	if err != nil {
		return nil, err
	}

	return append(issueUris, pullUris...), nil
}

func findIssueReferences(e Execer, refLinks []models.ReferenceLink) ([]syntax.ATURI, error) {
	if len(refLinks) == 0 {
		return nil, nil
	}
	vals := make([]string, len(refLinks))
	args := make([]any, 0, len(refLinks)*4)
	for i, ref := range refLinks {
		vals[i] = "(?, ?, ?, ?)"
		args = append(args, ref.Handle, ref.Repo, ref.SubjectId, ref.CommentId)
	}
	query := fmt.Sprintf(
		`with input(owner_did, name, issue_id, comment_id) as (
			values %s
		)
		select
			i.did, i.rkey,
			c.did, c.rkey
		from input inp
		join repos r
			on r.did = inp.owner_did
				and r.name = inp.name
		join issues i
			on i.repo_at = r.at_uri
				and i.issue_id = inp.issue_id
		left join issue_comments c
			on inp.comment_id is not null
				and c.issue_at = i.at_uri
				and c.id = inp.comment_id
		`,
		strings.Join(vals, ","),
	)
	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var uris []syntax.ATURI

	for rows.Next() {
		// Scan rows
		var issueOwner, issueRkey string
		var commentOwner, commentRkey sql.NullString
		var uri syntax.ATURI
		if err := rows.Scan(&issueOwner, &issueRkey, &commentOwner, &commentRkey); err != nil {
			return nil, err
		}
		if commentOwner.Valid && commentRkey.Valid {
			uri = syntax.ATURI(fmt.Sprintf(
				"at://%s/%s/%s",
				commentOwner.String,
				tangled.RepoIssueCommentNSID,
				commentRkey.String,
			))
		} else {
			uri = syntax.ATURI(fmt.Sprintf(
				"at://%s/%s/%s",
				issueOwner,
				tangled.RepoIssueNSID,
				issueRkey,
			))
		}
		uris = append(uris, uri)
	}
	return uris, nil
}

func findPullReferences(e Execer, refLinks []models.ReferenceLink) ([]syntax.ATURI, error) {
	if len(refLinks) == 0 {
		return nil, nil
	}
	vals := make([]string, len(refLinks))
	args := make([]any, 0, len(refLinks)*4)
	for i, ref := range refLinks {
		vals[i] = "(?, ?, ?, ?)"
		args = append(args, ref.Handle, ref.Repo, ref.SubjectId, ref.CommentId)
	}
	query := fmt.Sprintf(
		`with input(owner_did, name, pull_id, comment_id) as (
			values %s
		)
		select
			p.owner_did, p.rkey,
			c.owner_did, c.rkey
		from input inp
		join repos r
			on r.did = inp.owner_did
				and r.name = inp.name
		join pulls p
			on p.repo_at = r.at_uri
				and p.pull_id = inp.pull_id
		left join pull_comments c
			on inp.comment_id is not null
				and c.repo_at = r.at_uri and c.pull_id = p.pull_id
				and c.id = inp.comment_id
		`,
		strings.Join(vals, ","),
	)
	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var uris []syntax.ATURI

	for rows.Next() {
		// Scan rows
		var pullOwner, pullRkey string
		var commentOwner, commentRkey sql.NullString
		var uri syntax.ATURI
		if err := rows.Scan(&pullOwner, &pullRkey, &commentOwner, &commentRkey); err != nil {
			return nil, err
		}
		if commentOwner.Valid && commentRkey.Valid {
			uri = syntax.ATURI(fmt.Sprintf(
				"at://%s/%s/%s",
				commentOwner.String,
				tangled.RepoPullCommentNSID,
				commentRkey.String,
			))
		} else {
			uri = syntax.ATURI(fmt.Sprintf(
				"at://%s/%s/%s",
				pullOwner,
				tangled.RepoPullNSID,
				pullRkey,
			))
		}
		uris = append(uris, uri)
	}
	return uris, nil
}
