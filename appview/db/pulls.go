package db

import (
	"database/sql"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Pulls struct {
	ID       int       `json:"id"`
	OwnerDid string    `json:"owner_did"`
	RepoAt   string    `json:"repo_at"`
	PullId   int       `json:"pull_id"`
	Title    string    `json:"title"`
	Patch    string    `json:"patch,omitempty"`
	PatchAt  string    `json:"patch_at"`
	Open     int       `json:"open"`
	Created  time.Time `json:"created"`
}

type PullComments struct {
	ID        int       `json:"id"`
	OwnerDid  string    `json:"owner_did"`
	PullId    int       `json:"pull_id"`
	RepoAt    string    `json:"repo_at"`
	CommentId int       `json:"comment_id"`
	CommentAt string    `json:"comment_at"`
	Body      string    `json:"body"`
	Created   time.Time `json:"created"`
}

func NewPull(tx *sql.Tx, pull *Pulls) error {
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
		insert into pulls (repo_at, owner_did, pull_id, title, patch)
		values (?, ?, ?, ?, ?)
	`, pull.RepoAt, pull.OwnerDid, pull.PullId, pull.Title, pull.Patch)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func SetPullAt(e Execer, repoAt syntax.ATURI, pullId int, pullAt string) error {
	_, err := e.Exec(`update pulls set patch_at = ? where repo_at = ? and pull_id = ?`, pullAt, repoAt, pullId)
	return err
}

func GetPullAt(e Execer, repoAt syntax.ATURI, pullId int) (string, error) {
	var pullAt string
	err := e.QueryRow(`select patch_at from pulls where repo_at = ? and pull_id = ?`, repoAt, pullId).Scan(&pullAt)
	return pullAt, err
}

func GetPullId(e Execer, repoAt syntax.ATURI) (int, error) {
	var pullId int
	err := e.QueryRow(`select next_pull_id from repo_pull_seqs where repo_at = ?`, repoAt).Scan(&pullId)
	return pullId - 1, err
}

func GetPullOwnerDid(e Execer, repoAt syntax.ATURI, pullId int) (string, error) {
	var ownerDid string
	err := e.QueryRow(`select owner_did from pulls where repo_at = ? and pull_id = ?`, repoAt, pullId).Scan(&ownerDid)
	return ownerDid, err
}

func GetPulls(e Execer, repoAt syntax.ATURI) ([]Pulls, error) {
	var pulls []Pulls

	rows, err := e.Query(`select owner_did, pull_id, created, title, patch, open from pulls where repo_at = ? order by created desc`, repoAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pull Pulls
		var createdAt string
		err := rows.Scan(&pull.OwnerDid, &pull.PullId, &createdAt, &pull.Title, &pull.Patch, &pull.Open)
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

func GetPull(e Execer, repoAt syntax.ATURI, pullId int) (*Pulls, error) {
	query := `select owner_did, created, title, patch, open from pulls where repo_at = ? and pull_id = ?`
	row := e.QueryRow(query, repoAt, pullId)

	var pull Pulls
	var createdAt string
	err := row.Scan(&pull.OwnerDid, &createdAt, &pull.Title, &pull.Patch, &pull.Open)
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

func GetPullWithComments(e Execer, repoAt syntax.ATURI, pullId int) (*Pulls, []PullComments, error) {
	query := `select owner_did, pull_id, created, title, patch, open from pulls where repo_at = ? and pull_id = ?`
	row := e.QueryRow(query, repoAt, pullId)

	var pull Pulls
	var createdAt string
	err := row.Scan(&pull.OwnerDid, &pull.PullId, &createdAt, &pull.Title, &pull.Patch, &pull.Open)
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

func NewPullComment(e Execer, comment *PullComments) error {
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

func GetPullComments(e Execer, repoAt syntax.ATURI, pullId int) ([]PullComments, error) {
	var comments []PullComments

	rows, err := e.Query(`select owner_did, pull_id, comment_id, comment_at, body, created from pull_comments where repo_at = ? and pull_id = ? order by created asc`, repoAt, pullId)
	if err == sql.ErrNoRows {
		return []PullComments{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var comment PullComments
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

func ClosePull(e Execer, repoAt syntax.ATURI, pullId int) error {
	_, err := e.Exec(`update pulls set open = 0 where repo_at = ? and pull_id = ?`, repoAt, pullId)
	return err
}

func ReopenPull(e Execer, repoAt syntax.ATURI, pullId int) error {
	_, err := e.Exec(`update pulls set open = 1 where repo_at = ? and pull_id = ?`, repoAt, pullId)
	return err
}

type PullCount struct {
	Open   int
	Closed int
}

func GetPullCount(e Execer, repoAt syntax.ATURI) (PullCount, error) {
	row := e.QueryRow(`
		select
			count(case when open = 1 then 1 end) as open_count,
			count(case when open = 0 then 1 end) as closed_count
		from pulls
		where repo_at = ?`,
		repoAt,
	)

	var count PullCount
	if err := row.Scan(&count.Open, &count.Closed); err != nil {
		return PullCount{0, 0}, err
	}

	return count, nil
}
