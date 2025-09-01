package db

import (
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/pagination"
)

type Issue struct {
	Id      int64
	Did     string
	Rkey    string
	RepoAt  syntax.ATURI
	IssueId int
	Created time.Time
	Edited  *time.Time
	Deleted *time.Time
	Title   string
	Body    string
	Open    bool

	// optionally, populate this when querying for reverse mappings
	// like comment counts, parent repo etc.
	Comments []IssueComment
	Repo     *Repo
}

func (i *Issue) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", i.Did, tangled.RepoIssueNSID, i.Rkey))
}

func (i *Issue) AsRecord() tangled.RepoIssue {
	return tangled.RepoIssue{
		Repo:      i.RepoAt.String(),
		Title:     i.Title,
		Body:      &i.Body,
		CreatedAt: i.Created.Format(time.RFC3339),
	}
}

type CommentListItem struct {
	Self    *IssueComment
	Replies []*IssueComment
}

func (i *Issue) CommentList() []CommentListItem {
	// Create a map to quickly find comments by their aturi
	toplevel := make(map[string]*CommentListItem)
	var replies []*IssueComment

	// collect top level comments into the map
	for _, comment := range i.Comments {
		if comment.IsTopLevel() {
			toplevel[comment.AtUri().String()] = &CommentListItem{
				Self: &comment,
			}
		} else {
			replies = append(replies, &comment)
		}
	}

	for _, r := range replies {
		parentAt := *r.ReplyTo
		if parent, exists := toplevel[parentAt]; exists {
			parent.Replies = append(parent.Replies, r)
		}
	}

	var listing []CommentListItem
	for _, v := range toplevel {
		listing = append(listing, *v)
	}

	// sort everything
	sortFunc := func(a, b *IssueComment) bool {
		return a.Created.Before(b.Created)
	}
	sort.Slice(listing, func(i, j int) bool {
		return sortFunc(listing[i].Self, listing[j].Self)
	})
	for _, r := range listing {
		sort.Slice(r.Replies, func(i, j int) bool {
			return sortFunc(r.Replies[i], r.Replies[j])
		})
	}

	return listing
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
		RepoAt:  syntax.ATURI(record.Repo),
		Did:     did,
		Rkey:    rkey,
		Created: created,
		Title:   record.Title,
		Body:    body,
		Open:    true, // new issues are open by default
	}
}

type IssueComment struct {
	Id      int64
	Did     string
	Rkey    string
	IssueAt string
	ReplyTo *string
	Body    string
	Created time.Time
	Edited  *time.Time
	Deleted *time.Time
}

func (i *IssueComment) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", i.Did, tangled.RepoIssueCommentNSID, i.Rkey))
}

func (i *IssueComment) AsRecord() tangled.RepoIssueComment {
	return tangled.RepoIssueComment{
		Body:      i.Body,
		Issue:     i.IssueAt,
		CreatedAt: i.Created.Format(time.RFC3339),
		ReplyTo:   i.ReplyTo,
	}
}

func (i *IssueComment) IsTopLevel() bool {
	return i.ReplyTo == nil
}

func IssueCommentFromRecord(e Execer, did, rkey string, record tangled.RepoIssueComment) (*IssueComment, error) {
	created, err := time.Parse(time.RFC3339, record.CreatedAt)
	if err != nil {
		created = time.Now()
	}

	ownerDid := did

	if _, err = syntax.ParseATURI(record.Issue); err != nil {
		return nil, err
	}

	comment := IssueComment{
		Did:     ownerDid,
		Rkey:    rkey,
		Body:    record.Body,
		IssueAt: record.Issue,
		ReplyTo: record.ReplyTo,
		Created: created,
	}

	return &comment, nil
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
