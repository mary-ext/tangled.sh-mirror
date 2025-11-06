package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/models"
)

// ValidateReferenceLinks resolves refLinks to Issue/PR/IssueComment/PullComment ATURIs.
// It will ignore missing refLinks.
func ValidateReferenceLinks(e Execer, refLinks []models.ReferenceLink) ([]syntax.ATURI, error) {
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
		return nil, fmt.Errorf("find issue references: %w", err)
	}
	pullUris, err := findPullReferences(e, pullRefs)
	if err != nil {
		return nil, fmt.Errorf("find pull references: %w", err)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
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
			c.comment_at
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
		var commentUri sql.NullString
		var uri syntax.ATURI
		if err := rows.Scan(&pullOwner, &pullRkey, &commentUri); err != nil {
			return nil, err
		}
		if commentUri.Valid {
			// no-op
			uri = syntax.ATURI(commentUri.String)
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

func putReferences(tx *sql.Tx, fromAt syntax.ATURI, references []syntax.ATURI) error {
	err := deleteReferences(tx, fromAt)
	if err != nil {
		return fmt.Errorf("delete old reference_links: %w", err)
	}
	if len(references) == 0 {
		return nil
	}

	values := make([]string, 0, len(references))
	args := make([]any, 0, len(references)*2)
	for _, ref := range references {
		values = append(values, "(?, ?)")
		args = append(args, fromAt, ref)
	}
	_, err = tx.Exec(
		fmt.Sprintf(
			`insert into reference_links (from_at, to_at)
			values %s`,
			strings.Join(values, ","),
		),
		args...,
	)
	if err != nil {
		return fmt.Errorf("insert new reference_links: %w", err)
	}
	return nil
}

func deleteReferences(tx *sql.Tx, fromAt syntax.ATURI) error {
	_, err := tx.Exec(`delete from reference_links where from_at = ?`, fromAt)
	return err
}

func GetReferencesAll(e Execer, filters ...filter) (map[syntax.ATURI][]syntax.ATURI, error) {
	var (
		conditions []string
		args       []any
	)
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	rows, err := e.Query(
		fmt.Sprintf(
			`select from_at, to_at from reference_links %s`,
			whereClause,
		),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query reference_links: %w", err)
	}
	defer rows.Close()

	result := make(map[syntax.ATURI][]syntax.ATURI)

	for rows.Next() {
		var from, to syntax.ATURI
		if err := rows.Scan(&from, &to); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		result[from] = append(result[from], to)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return result, nil
}

func GetBacklinks(e Execer, target syntax.ATURI) ([]models.RichReferenceLink, error) {
	rows, err := e.Query(
		`select from_at from reference_links
		where to_at = ?`,
		target,
	)
	if err != nil {
		return nil, fmt.Errorf("query backlinks: %w", err)
	}
	defer rows.Close()

	var (
		backlinks    []models.RichReferenceLink
		backlinksMap = make(map[string][]syntax.ATURI)
	)
	for rows.Next() {
		var from syntax.ATURI
		if err := rows.Scan(&from); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		nsid := from.Collection().String()
		backlinksMap[nsid] = append(backlinksMap[nsid], from)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	var ls []models.RichReferenceLink
	ls, err = getIssueBacklinks(e, backlinksMap[tangled.RepoIssueNSID])
	if err != nil {
		return nil, fmt.Errorf("get issue backlinks: %w", err)
	}
	backlinks = append(backlinks, ls...)
	ls, err = getIssueCommentBacklinks(e, backlinksMap[tangled.RepoIssueCommentNSID])
	if err != nil {
		return nil, fmt.Errorf("get issue_comment backlinks: %w", err)
	}
	backlinks = append(backlinks, ls...)
	ls, err = getPullBacklinks(e, backlinksMap[tangled.RepoPullNSID])
	if err != nil {
		return nil, fmt.Errorf("get pull backlinks: %w", err)
	}
	backlinks = append(backlinks, ls...)
	ls, err = getPullCommentBacklinks(e, backlinksMap[tangled.RepoPullCommentNSID])
	if err != nil {
		return nil, fmt.Errorf("get pull_comment backlinks: %w", err)
	}
	backlinks = append(backlinks, ls...)

	return backlinks, nil
}

func getIssueBacklinks(e Execer, aturis []syntax.ATURI) ([]models.RichReferenceLink, error) {
	if len(aturis) == 0 {
		return nil, nil
	}
	vals := make([]string, len(aturis))
	args := make([]any, 0, len(aturis)*2)
	for i, aturi := range aturis {
		vals[i] = "(?, ?)"
		did := aturi.Authority().String()
		rkey := aturi.RecordKey().String()
		args = append(args, did, rkey)
	}
	rows, err := e.Query(
		fmt.Sprintf(
			`select r.did, r.name, i.issue_id, i.title, i.open
			from issues i
			join repos r
				on r.at_uri = i.repo_at
			where (i.did, i.rkey) in (%s)`,
			strings.Join(vals, ","),
		),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refLinks []models.RichReferenceLink
	for rows.Next() {
		var l models.RichReferenceLink
		l.Kind = models.RefKindIssue
		if err := rows.Scan(&l.Handle, &l.Repo, &l.SubjectId, &l.Title, &l.State); err != nil {
			return nil, err
		}
		refLinks = append(refLinks, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return refLinks, nil
}

func getIssueCommentBacklinks(e Execer, aturis []syntax.ATURI) ([]models.RichReferenceLink, error) {
	if len(aturis) == 0 {
		return nil, nil
	}
	filter := FilterIn("c.at_uri", aturis)
	rows, err := e.Query(
		fmt.Sprintf(
			`select r.did, r.name, i.issue_id, c.id, i.title, i.open
			from issue_comments c
			join issues i
				on i.at_uri = c.issue_at
			join repos r
				on r.at_uri = i.repo_at
			where %s`,
			filter.Condition(),
		),
		filter.Arg()...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refLinks []models.RichReferenceLink
	for rows.Next() {
		var l models.RichReferenceLink
		l.Kind = models.RefKindIssue
		l.CommentId = new(int)
		if err := rows.Scan(&l.Handle, &l.Repo, &l.SubjectId, l.CommentId, &l.Title, &l.State); err != nil {
			return nil, err
		}
		refLinks = append(refLinks, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return refLinks, nil
}

func getPullBacklinks(e Execer, aturis []syntax.ATURI) ([]models.RichReferenceLink, error) {
	if len(aturis) == 0 {
		return nil, nil
	}
	vals := make([]string, len(aturis))
	args := make([]any, 0, len(aturis)*2)
	for i, aturi := range aturis {
		vals[i] = "(?, ?)"
		did := aturi.Authority().String()
		rkey := aturi.RecordKey().String()
		args = append(args, did, rkey)
	}
	rows, err := e.Query(
		fmt.Sprintf(
			`select r.did, r.name, p.pull_id, p.title, p.state
			from pulls p
			join repos r
				on r.at_uri = p.repo_at
			where (p.owner_did, p.rkey) in (%s)`,
			strings.Join(vals, ","),
		),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refLinks []models.RichReferenceLink
	for rows.Next() {
		var l models.RichReferenceLink
		l.Kind = models.RefKindPull
		if err := rows.Scan(&l.Handle, &l.Repo, &l.SubjectId, &l.Title, &l.State); err != nil {
			return nil, err
		}
		refLinks = append(refLinks, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return refLinks, nil
}

func getPullCommentBacklinks(e Execer, aturis []syntax.ATURI) ([]models.RichReferenceLink, error) {
	if len(aturis) == 0 {
		return nil, nil
	}
	filter := FilterIn("c.comment_at", aturis)
	rows, err := e.Query(
		fmt.Sprintf(
			`select r.did, r.name, p.pull_id, c.id, p.title, p.state
			from repos r
			join pulls p
				on r.at_uri = p.repo_at
			join pull_comments c
				on r.at_uri = c.repo_at and p.pull_id = c.pull_id
			where %s`,
			filter.Condition(),
		),
		filter.Arg()...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refLinks []models.RichReferenceLink
	for rows.Next() {
		var l models.RichReferenceLink
		l.Kind = models.RefKindPull
		l.CommentId = new(int)
		if err := rows.Scan(&l.Handle, &l.Repo, &l.SubjectId, l.CommentId, &l.Title, &l.State); err != nil {
			return nil, err
		}
		refLinks = append(refLinks, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return refLinks, nil
}
