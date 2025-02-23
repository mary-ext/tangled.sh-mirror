package db

import "time"

type Issue struct {
	RepoAt   string
	OwnerDid string
	IssueId  int
	Created  *time.Time
	Title    string
	Body     string
	Open     bool
}

type Comment struct {
	OwnerDid  string
	RepoAt    string
	Issue     int
	CommentId int
	Body      string
	Created   *time.Time
}

func (d *DB) NewIssue(issue *Issue) (int, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		insert or ignore into repo_issue_seqs (repo_at, next_issue_id)
		values (?, 1)
		`, issue.RepoAt)
	if err != nil {
		return 0, err
	}

	var nextId int
	err = tx.QueryRow(`
		update repo_issue_seqs
		set next_issue_id = next_issue_id + 1
		where repo_at = ?
		returning next_issue_id - 1
		`, issue.RepoAt).Scan(&nextId)
	if err != nil {
		return 0, err
	}

	issue.IssueId = nextId

	_, err = tx.Exec(`
		insert into issues (repo_at, owner_did, issue_id, title, body)
		values (?, ?, ?, ?, ?)
	`, issue.RepoAt, issue.OwnerDid, issue.IssueId, issue.Title, issue.Body)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return nextId, nil
}

func (d *DB) GetIssues(repoAt string) ([]Issue, error) {
	var issues []Issue

	rows, err := d.db.Query(`select owner_did, issue_id, created, title, body, open from issues where repo_at = ?`, repoAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var issue Issue
		var createdAt string
		err := rows.Scan(&issue.OwnerDid, &issue.IssueId, &createdAt, &issue.Title, &issue.Body, &issue.Open)
		if err != nil {
			return nil, err
		}

		createdTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		issue.Created = &createdTime

		issues = append(issues, issue)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return issues, nil
}

func (d *DB) GetIssueWithComments(repoAt string, issueId int) (*Issue, []Comment, error) {
	query := `select owner_did, issue_id, created, title, body, open from issues where repo_at = ? and issue_id = ?`
	row := d.db.QueryRow(query, repoAt, issueId)

	var issue Issue
	var createdAt string
	err := row.Scan(&issue.OwnerDid, &issue.IssueId, &createdAt, &issue.Title, &issue.Body, &issue.Open)
	if err != nil {
		return nil, nil, err
	}

	createdTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, nil, err
	}
	issue.Created = &createdTime

	comments, err := d.GetComments(repoAt, issueId)
	if err != nil {
		return nil, nil, err
	}

	return &issue, comments, nil
}

func (d *DB) NewComment(comment *Comment) error {
	query := `insert into comments (owner_did, repo_at, issue_id, comment_id, body) values (?, ?, ?, ?, ?)`
	_, err := d.db.Exec(
		query,
		comment.OwnerDid,
		comment.RepoAt,
		comment.Issue,
		comment.CommentId,
		comment.Body,
	)
	return err
}

func (d *DB) GetComments(repoAt string, issueId int) ([]Comment, error) {
	var comments []Comment

	rows, err := d.db.Query(`select owner_did, issue_id, comment_id, body, created from comments where repo_at = ? and issue_id = ? order by created asc`, repoAt, issueId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var comment Comment
		var createdAt string
		err := rows.Scan(&comment.OwnerDid, &comment.Issue, &comment.CommentId, &comment.Body, &createdAt)
		if err != nil {
			return nil, err
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		comment.Created = &createdAtTime

		comments = append(comments, comment)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return comments, nil
}

func (d *DB) CloseIssue(repoAt string, issueId int) error {
	_, err := d.db.Exec(`update issues set open = 0 where repo_at = ? and issue_id = ?`, repoAt, issueId)
	return err
}

func (d *DB) ReopenIssue(repoAt string, issueId int) error {
	_, err := d.db.Exec(`update issues set open = 1 where repo_at = ? and issue_id = ?`, repoAt, issueId)
	return err
}
