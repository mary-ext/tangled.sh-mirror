package db

import (
	"database/sql"
	"fmt"
	mathrand "math/rand/v2"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/pagination"
)

type Issue struct {
	ID       int64
	RepoAt   syntax.ATURI
	OwnerDid string
	IssueId  int
	Rkey     string
	Created  time.Time
	Title    string
	Body     string
	Open     bool

	// optionally, populate this when querying for reverse mappings
	// like comment counts, parent repo etc.
	Metadata *IssueMetadata
}

type IssueMetadata struct {
	CommentCount int
	Repo         *Repo
	// labels, assignee etc.
}

type Comment struct {
	OwnerDid  string
	RepoAt    syntax.ATURI
	Rkey      string
	Issue     int
	CommentId int
	Body      string
	Created   *time.Time
	Deleted   *time.Time
	Edited    *time.Time
}

func (i *Issue) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", i.OwnerDid, tangled.RepoIssueNSID, i.Rkey))
}

func IssueFromRecord(did, rkey string, record tangled.RepoIssue) Issue {
	created, err := time.Parse(time.RFC3339, record.CreatedAt)
	if err != nil {
		created = time.Now()
	}

	body := ""
	if record.Body != nil {
		body = *record.Body
	}

	return Issue{
		RepoAt:   syntax.ATURI(record.Repo),
		OwnerDid: record.Owner,
		Rkey:     rkey,
		Created:  created,
		Title:    record.Title,
		Body:     body,
		Open:     true, // new issues are open by default
	}
}

func ResolveIssueFromAtUri(e Execer, issueUri syntax.ATURI) (syntax.ATURI, int, error) {
	ownerDid := issueUri.Authority().String()
	issueRkey := issueUri.RecordKey().String()

	var repoAt string
	var issueId int

	query := `select repo_at, issue_id from issues where owner_did = ? and rkey = ?`
	err := e.QueryRow(query, ownerDid, issueRkey).Scan(&repoAt, &issueId)
	if err != nil {
		return "", 0, err
	}

	return syntax.ATURI(repoAt), issueId, nil
}

func IssueCommentFromRecord(e Execer, did, rkey string, record tangled.RepoIssueComment) (Comment, error) {
	created, err := time.Parse(time.RFC3339, record.CreatedAt)
	if err != nil {
		created = time.Now()
	}

	ownerDid := did
	if record.Owner != nil {
		ownerDid = *record.Owner
	}

	issueUri, err := syntax.ParseATURI(record.Issue)
	if err != nil {
		return Comment{}, err
	}

	repoAt, issueId, err := ResolveIssueFromAtUri(e, issueUri)
	if err != nil {
		return Comment{}, err
	}

	comment := Comment{
		OwnerDid:  ownerDid,
		RepoAt:    repoAt,
		Rkey:      rkey,
		Body:      record.Body,
		Issue:     issueId,
		CommentId: mathrand.IntN(1000000),
		Created:   &created,
	}

	return comment, nil
}

func NewIssue(tx *sql.Tx, issue *Issue) error {
	defer tx.Rollback()

	_, err := tx.Exec(`
		insert or ignore into repo_issue_seqs (repo_at, next_issue_id)
		values (?, 1)
		`, issue.RepoAt)
	if err != nil {
		return err
	}

	var nextId int
	err = tx.QueryRow(`
		update repo_issue_seqs
		set next_issue_id = next_issue_id + 1
		where repo_at = ?
		returning next_issue_id - 1
		`, issue.RepoAt).Scan(&nextId)
	if err != nil {
		return err
	}

	issue.IssueId = nextId

	res, err := tx.Exec(`
		insert into issues (repo_at, owner_did, rkey, issue_at, issue_id, title, body)
		values (?, ?, ?, ?, ?, ?, ?)
	`, issue.RepoAt, issue.OwnerDid, issue.Rkey, issue.AtUri(), issue.IssueId, issue.Title, issue.Body)
	if err != nil {
		return err
	}

	lastID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	issue.ID = lastID

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func GetIssueAt(e Execer, repoAt syntax.ATURI, issueId int) (string, error) {
	var issueAt string
	err := e.QueryRow(`select issue_at from issues where repo_at = ? and issue_id = ?`, repoAt, issueId).Scan(&issueAt)
	return issueAt, err
}

func GetIssueOwnerDid(e Execer, repoAt syntax.ATURI, issueId int) (string, error) {
	var ownerDid string
	err := e.QueryRow(`select owner_did from issues where repo_at = ? and issue_id = ?`, repoAt, issueId).Scan(&ownerDid)
	return ownerDid, err
}

func GetIssuesPaginated(e Execer, repoAt syntax.ATURI, isOpen bool, page pagination.Page) ([]Issue, error) {
	var issues []Issue
	openValue := 0
	if isOpen {
		openValue = 1
	}

	rows, err := e.Query(
		`
		with numbered_issue as (
			select
				i.id,
				i.owner_did,
				i.rkey,
				i.issue_id,
				i.created,
				i.title,
				i.body,
				i.open,
				count(c.id) as comment_count,
				row_number() over (order by i.created desc) as row_num
			from
				issues i
			left join
				comments c on i.repo_at = c.repo_at and i.issue_id = c.issue_id
			where
				i.repo_at = ? and i.open = ?
			group by
				i.id, i.owner_did, i.issue_id, i.created, i.title, i.body, i.open
		)
		select
			id,
			owner_did,
			rkey,
			issue_id,
			created,
			title,
			body,
			open,
			comment_count
		from
			numbered_issue
		where
			row_num between ? and ?`,
		repoAt, openValue, page.Offset+1, page.Offset+page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var issue Issue
		var createdAt string
		var metadata IssueMetadata
		err := rows.Scan(&issue.ID, &issue.OwnerDid, &issue.Rkey, &issue.IssueId, &createdAt, &issue.Title, &issue.Body, &issue.Open, &metadata.CommentCount)
		if err != nil {
			return nil, err
		}

		createdTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		issue.Created = createdTime
		issue.Metadata = &metadata

		issues = append(issues, issue)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return issues, nil
}

func GetIssuesWithLimit(e Execer, limit int, filters ...filter) ([]Issue, error) {
	issues := make([]Issue, 0, limit)

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
	limitClause := ""
	if limit != 0 {
		limitClause = fmt.Sprintf(" limit %d ", limit)
	}

	query := fmt.Sprintf(
		`select
			i.id,
			i.owner_did,
			i.repo_at,
			i.issue_id,
			i.created,
			i.title,
			i.body,
			i.open
		from
		    issues i
		%s
		order by
			i.created desc
		%s`,
		whereClause, limitClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var issue Issue
		var issueCreatedAt string
		err := rows.Scan(
			&issue.ID,
			&issue.OwnerDid,
			&issue.RepoAt,
			&issue.IssueId,
			&issueCreatedAt,
			&issue.Title,
			&issue.Body,
			&issue.Open,
		)
		if err != nil {
			return nil, err
		}

		issueCreatedTime, err := time.Parse(time.RFC3339, issueCreatedAt)
		if err != nil {
			return nil, err
		}
		issue.Created = issueCreatedTime

		issues = append(issues, issue)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return issues, nil
}

func GetIssues(e Execer, filters ...filter) ([]Issue, error) {
	return GetIssuesWithLimit(e, 0, filters...)
}

// timeframe here is directly passed into the sql query filter, and any
// timeframe in the past should be negative; e.g.: "-3 months"
func GetIssuesByOwnerDid(e Execer, ownerDid string, timeframe string) ([]Issue, error) {
	var issues []Issue

	rows, err := e.Query(
		`select
			i.id,
			i.owner_did,
			i.rkey,
			i.repo_at,
			i.issue_id,
			i.created,
			i.title,
			i.body,
			i.open,
			r.did,
			r.name,
			r.knot,
			r.rkey,
			r.created
		from
		    issues i
		join
			repos r on i.repo_at = r.at_uri
		where
			i.owner_did = ? and i.created >= date ('now', ?)
		order by
			i.created desc`,
		ownerDid, timeframe)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var issue Issue
		var issueCreatedAt, repoCreatedAt string
		var repo Repo
		err := rows.Scan(
			&issue.ID,
			&issue.OwnerDid,
			&issue.Rkey,
			&issue.RepoAt,
			&issue.IssueId,
			&issueCreatedAt,
			&issue.Title,
			&issue.Body,
			&issue.Open,
			&repo.Did,
			&repo.Name,
			&repo.Knot,
			&repo.Rkey,
			&repoCreatedAt,
		)
		if err != nil {
			return nil, err
		}

		issueCreatedTime, err := time.Parse(time.RFC3339, issueCreatedAt)
		if err != nil {
			return nil, err
		}
		issue.Created = issueCreatedTime

		repoCreatedTime, err := time.Parse(time.RFC3339, repoCreatedAt)
		if err != nil {
			return nil, err
		}
		repo.Created = repoCreatedTime

		issue.Metadata = &IssueMetadata{
			Repo: &repo,
		}

		issues = append(issues, issue)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return issues, nil
}

func GetIssue(e Execer, repoAt syntax.ATURI, issueId int) (*Issue, error) {
	query := `select id, owner_did, rkey, created, title, body, open from issues where repo_at = ? and issue_id = ?`
	row := e.QueryRow(query, repoAt, issueId)

	var issue Issue
	var createdAt string
	err := row.Scan(&issue.ID, &issue.OwnerDid, &issue.Rkey, &createdAt, &issue.Title, &issue.Body, &issue.Open)
	if err != nil {
		return nil, err
	}

	createdTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, err
	}
	issue.Created = createdTime

	return &issue, nil
}

func GetIssueWithComments(e Execer, repoAt syntax.ATURI, issueId int) (*Issue, []Comment, error) {
	query := `select id, owner_did, rkey, issue_id, created, title, body, open from issues where repo_at = ? and issue_id = ?`
	row := e.QueryRow(query, repoAt, issueId)

	var issue Issue
	var createdAt string
	err := row.Scan(&issue.ID, &issue.OwnerDid, &issue.Rkey, &issue.IssueId, &createdAt, &issue.Title, &issue.Body, &issue.Open)
	if err != nil {
		return nil, nil, err
	}

	createdTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, nil, err
	}
	issue.Created = createdTime

	comments, err := GetComments(e, repoAt, issueId)
	if err != nil {
		return nil, nil, err
	}

	return &issue, comments, nil
}

func NewIssueComment(e Execer, comment *Comment) error {
	query := `insert into comments (owner_did, repo_at, rkey, issue_id, comment_id, body) values (?, ?, ?, ?, ?, ?)`
	_, err := e.Exec(
		query,
		comment.OwnerDid,
		comment.RepoAt,
		comment.Rkey,
		comment.Issue,
		comment.CommentId,
		comment.Body,
	)
	return err
}

func GetComments(e Execer, repoAt syntax.ATURI, issueId int) ([]Comment, error) {
	var comments []Comment

	rows, err := e.Query(`
		select
			owner_did,
			issue_id,
			comment_id,
			rkey,
			body,
			created,
			edited,
			deleted
		from
			comments
		where
			repo_at = ? and issue_id = ?
		order by
			created asc`,
		repoAt,
		issueId,
	)
	if err == sql.ErrNoRows {
		return []Comment{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var comment Comment
		var createdAt string
		var deletedAt, editedAt, rkey sql.NullString
		err := rows.Scan(&comment.OwnerDid, &comment.Issue, &comment.CommentId, &rkey, &comment.Body, &createdAt, &editedAt, &deletedAt)
		if err != nil {
			return nil, err
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		comment.Created = &createdAtTime

		if deletedAt.Valid {
			deletedTime, err := time.Parse(time.RFC3339, deletedAt.String)
			if err != nil {
				return nil, err
			}
			comment.Deleted = &deletedTime
		}

		if editedAt.Valid {
			editedTime, err := time.Parse(time.RFC3339, editedAt.String)
			if err != nil {
				return nil, err
			}
			comment.Edited = &editedTime
		}

		if rkey.Valid {
			comment.Rkey = rkey.String
		}

		comments = append(comments, comment)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return comments, nil
}

func GetComment(e Execer, repoAt syntax.ATURI, issueId, commentId int) (*Comment, error) {
	query := `
		select
			owner_did, body, rkey, created, deleted, edited
		from
			comments where repo_at = ? and issue_id = ? and comment_id = ?
	`
	row := e.QueryRow(query, repoAt, issueId, commentId)

	var comment Comment
	var createdAt string
	var deletedAt, editedAt, rkey sql.NullString
	err := row.Scan(&comment.OwnerDid, &comment.Body, &rkey, &createdAt, &deletedAt, &editedAt)
	if err != nil {
		return nil, err
	}

	createdTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, err
	}
	comment.Created = &createdTime

	if deletedAt.Valid {
		deletedTime, err := time.Parse(time.RFC3339, deletedAt.String)
		if err != nil {
			return nil, err
		}
		comment.Deleted = &deletedTime
	}

	if editedAt.Valid {
		editedTime, err := time.Parse(time.RFC3339, editedAt.String)
		if err != nil {
			return nil, err
		}
		comment.Edited = &editedTime
	}

	if rkey.Valid {
		comment.Rkey = rkey.String
	}

	comment.RepoAt = repoAt
	comment.Issue = issueId
	comment.CommentId = commentId

	return &comment, nil
}

func EditComment(e Execer, repoAt syntax.ATURI, issueId, commentId int, newBody string) error {
	_, err := e.Exec(
		`
		update comments
		set body = ?,
			edited = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		where repo_at = ? and issue_id = ? and comment_id = ?
		`, newBody, repoAt, issueId, commentId)
	return err
}

func DeleteComment(e Execer, repoAt syntax.ATURI, issueId, commentId int) error {
	_, err := e.Exec(
		`
		update comments
		set body = "",
			deleted = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		where repo_at = ? and issue_id = ? and comment_id = ?
		`, repoAt, issueId, commentId)
	return err
}

func UpdateCommentByRkey(e Execer, ownerDid, rkey, newBody string) error {
	_, err := e.Exec(
		`
		update comments
		set body = ?,
			edited = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		where owner_did = ? and rkey = ?
		`, newBody, ownerDid, rkey)
	return err
}

func DeleteCommentByRkey(e Execer, ownerDid, rkey string) error {
	_, err := e.Exec(
		`
		update comments
		set body = "",
			deleted = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		where owner_did = ? and rkey = ?
		`, ownerDid, rkey)
	return err
}

func UpdateIssueByRkey(e Execer, ownerDid, rkey, title, body string) error {
	_, err := e.Exec(`update issues set title = ?, body = ? where owner_did = ? and rkey = ?`, title, body, ownerDid, rkey)
	return err
}

func DeleteIssueByRkey(e Execer, ownerDid, rkey string) error {
	_, err := e.Exec(`delete from issues where owner_did = ? and rkey = ?`, ownerDid, rkey)
	return err
}

func CloseIssue(e Execer, repoAt syntax.ATURI, issueId int) error {
	_, err := e.Exec(`update issues set open = 0 where repo_at = ? and issue_id = ?`, repoAt, issueId)
	return err
}

func ReopenIssue(e Execer, repoAt syntax.ATURI, issueId int) error {
	_, err := e.Exec(`update issues set open = 1 where repo_at = ? and issue_id = ?`, repoAt, issueId)
	return err
}

type IssueCount struct {
	Open   int
	Closed int
}

func GetIssueCount(e Execer, repoAt syntax.ATURI) (IssueCount, error) {
	row := e.QueryRow(`
		select
			count(case when open = 1 then 1 end) as open_count,
			count(case when open = 0 then 1 end) as closed_count
		from issues
		where repo_at = ?`,
		repoAt,
	)

	var count IssueCount
	if err := row.Scan(&count.Open, &count.Closed); err != nil {
		return IssueCount{0, 0}, err
	}

	return count, nil
}
