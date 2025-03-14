package db

import (
	"database/sql"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type PullState int

const (
	PullOpen PullState = iota
	PullMerged
	PullClosed
)

func (p PullState) String() string {
	switch p {
	case PullOpen:
		return "open"
	case PullMerged:
		return "merged"
	case PullClosed:
		return "closed"
	default:
		return "closed"
	}
}

func (p PullState) IsOpen() bool {
	return p == PullOpen
}
func (p PullState) IsMerged() bool {
	return p == PullMerged
}
func (p PullState) IsClosed() bool {
	return p == PullClosed
}

type Pull struct {
	ID           int
	OwnerDid     string
	RepoAt       syntax.ATURI
	PullAt       syntax.ATURI
	TargetBranch string
	Patch        string
	PullId       int
	Title        string
	Body         string
	State        PullState
	Created      time.Time
	Rkey         string
}

type PullComment struct {
	ID        int
	OwnerDid  string
	PullId    int
	RepoAt    string
	CommentId int
	CommentAt string
	Body      string
	Created   time.Time
}

func NewPull(tx *sql.Tx, pull *Pull) error {
	defer tx.Rollback()

	_, err := tx.Exec(`
		insert or ignore into repo_pull_seqs (repo_at, next_pull_id)
		values (?, 1)
		`, pull.RepoAt)
	if err != nil {
		return err
	}

	var nextId int
	err = tx.QueryRow(`
		update repo_pull_seqs
		set next_pull_id = next_pull_id + 1
		where repo_at = ?
		returning next_pull_id - 1
		`, pull.RepoAt).Scan(&nextId)
	if err != nil {
		return err
	}

	pull.PullId = nextId

	_, err = tx.Exec(`
		insert into pulls (repo_at, owner_did, pull_id, title, target_branch, body, patch, rkey)
		values (?, ?, ?, ?, ?, ?, ?, ?)
	`, pull.RepoAt, pull.OwnerDid, pull.PullId, pull.Title, pull.TargetBranch, pull.Body, pull.Patch, pull.Rkey)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func SetPullAt(e Execer, repoAt syntax.ATURI, pullId int, pullAt string) error {
	_, err := e.Exec(`update pulls set pull_at = ? where repo_at = ? and pull_id = ?`, pullAt, repoAt, pullId)
	return err
}

func GetPullAt(e Execer, repoAt syntax.ATURI, pullId int) (string, error) {
	var pullAt string
	err := e.QueryRow(`select pull_at from pulls where repo_at = ? and pull_id = ?`, repoAt, pullId).Scan(&pullAt)
	return pullAt, err
}

func NextPullId(e Execer, repoAt syntax.ATURI) (int, error) {
	var pullId int
	err := e.QueryRow(`select next_pull_id from repo_pull_seqs where repo_at = ?`, repoAt).Scan(&pullId)
	return pullId - 1, err
}

func GetPulls(e Execer, repoAt syntax.ATURI, state PullState) ([]Pull, error) {
	var pulls []Pull

	rows, err := e.Query(`
		select
			owner_did,
			pull_id,
			created,
			title,
			state,
			target_branch,
			pull_at,
			body,
			patch,
			rkey 
		from
			pulls
		where
			repo_at = ? and state = ?
		order by
			created desc`, repoAt, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pull Pull
		var createdAt string
		err := rows.Scan(&pull.OwnerDid, &pull.PullId, &createdAt, &pull.Title, &pull.State, &pull.TargetBranch, &pull.PullAt, &pull.Body, &pull.Patch, &pull.Rkey)
		if err != nil {
			return nil, err
		}

		createdTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		pull.Created = createdTime

		pulls = append(pulls, pull)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pulls, nil
}

func GetPull(e Execer, repoAt syntax.ATURI, pullId int) (*Pull, error) {
	query := `select owner_did, created, title, state, target_branch, pull_at, body, patch, rkey from pulls where repo_at = ? and pull_id = ?`
	row := e.QueryRow(query, repoAt, pullId)

	var pull Pull
	var createdAt string
	err := row.Scan(&pull.OwnerDid, &createdAt, &pull.Title, &pull.State, &pull.TargetBranch, &pull.PullAt, &pull.Body, &pull.Patch, &pull.Rkey)
	if err != nil {
		return nil, err
	}

	createdTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, err
	}
	pull.Created = createdTime

	return &pull, nil
}

func GetPullWithComments(e Execer, repoAt syntax.ATURI, pullId int) (*Pull, []PullComment, error) {
	query := `select owner_did, pull_id, created, title, state, target_branch, pull_at, body, patch, rkey from pulls where repo_at = ? and pull_id = ?`
	row := e.QueryRow(query, repoAt, pullId)

	var pull Pull
	var createdAt string
	err := row.Scan(&pull.OwnerDid, &pull.PullId, &createdAt, &pull.Title, &pull.State, &pull.TargetBranch, &pull.PullAt, &pull.Body, &pull.Patch, &pull.Rkey)
	if err != nil {
		return nil, nil, err
	}

	createdTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, nil, err
	}
	pull.Created = createdTime

	comments, err := GetPullComments(e, repoAt, pullId)
	if err != nil {
		return nil, nil, err
	}

	return &pull, comments, nil
}

func NewPullComment(e Execer, comment *PullComment) error {
	query := `insert into pull_comments (owner_did, repo_at, comment_at, pull_id, comment_id, body) values (?, ?, ?, ?, ?, ?)`
	_, err := e.Exec(
		query,
		comment.OwnerDid,
		comment.RepoAt,
		comment.CommentAt,
		comment.PullId,
		comment.CommentId,
		comment.Body,
	)
	return err
}

func GetPullComments(e Execer, repoAt syntax.ATURI, pullId int) ([]PullComment, error) {
	var comments []PullComment

	rows, err := e.Query(`select owner_did, pull_id, comment_id, comment_at, body, created from pull_comments where repo_at = ? and pull_id = ? order by created asc`, repoAt, pullId)
	if err == sql.ErrNoRows {
		return []PullComment{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var comment PullComment
		var createdAt string
		err := rows.Scan(&comment.OwnerDid, &comment.PullId, &comment.CommentId, &comment.CommentAt, &comment.Body, &createdAt)
		if err != nil {
			return nil, err
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		comment.Created = createdAtTime

		comments = append(comments, comment)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return comments, nil
}

func SetPullState(e Execer, repoAt syntax.ATURI, pullId int, pullState PullState) error {
	_, err := e.Exec(`update pulls set state = ? where repo_at = ? and pull_id = ?`, pullState, repoAt, pullId)
	return err
}

func ClosePull(e Execer, repoAt syntax.ATURI, pullId int) error {
	err := SetPullState(e, repoAt, pullId, PullClosed)
	return err
}

func ReopenPull(e Execer, repoAt syntax.ATURI, pullId int) error {
	err := SetPullState(e, repoAt, pullId, PullOpen)
	return err
}

func MergePull(e Execer, repoAt syntax.ATURI, pullId int) error {
	err := SetPullState(e, repoAt, pullId, PullMerged)
	return err
}

type PullCount struct {
	Open   int
	Merged int
	Closed int
}

func GetPullCount(e Execer, repoAt syntax.ATURI) (PullCount, error) {
	row := e.QueryRow(`
		select
			count(case when state = 0 then 1 end) as open_count,
			count(case when state = 1 then 1 end) as merged_count,
			count(case when state = 2 then 1 end) as closed_count
		from pulls
		where repo_at = ?`,
		repoAt,
	)

	var count PullCount
	if err := row.Scan(&count.Open, &count.Merged, &count.Closed); err != nil {
		return PullCount{0, 0, 0}, err
	}

	return count, nil
}

func EditPatch(e Execer, repoAt syntax.ATURI, pullId int, patch string) error {
	_, err := e.Exec(`update pulls set patch = ? where repo_at = ? and pull_id = ?`, patch, repoAt, pullId)
	return err
}
