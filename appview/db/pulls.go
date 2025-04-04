package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.sh/tangled.sh/core/types"
)

type PullState int

const (
	PullClosed PullState = iota
	PullOpen
	PullMerged
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
	// ids
	ID     int
	PullId int

	// at ids
	RepoAt   syntax.ATURI
	OwnerDid string
	Rkey     string
	PullAt   syntax.ATURI

	// content
	Title        string
	Body         string
	TargetBranch string
	State        PullState
	Submissions  []*PullSubmission

	// meta
	Created    time.Time
	PullSource *PullSource
}

type PullSource struct {
	Branch string
	Repo   *syntax.ATURI
}

type PullSubmission struct {
	// ids
	ID     int
	PullId int

	// at ids
	RepoAt syntax.ATURI

	// content
	RoundNumber int
	Patch       string
	Comments    []PullComment
	SourceRev   string // include the rev that was used to create this submission: only for branch PRs

	// meta
	Created time.Time
}

type PullComment struct {
	// ids
	ID           int
	PullId       int
	SubmissionId int

	// at ids
	RepoAt    string
	OwnerDid  string
	CommentAt string

	// content
	Body string

	// meta
	Created time.Time
}

func (p *Pull) LatestPatch() string {
	latestSubmission := p.Submissions[p.LastRoundNumber()]
	return latestSubmission.Patch
}

func (p *Pull) LastRoundNumber() int {
	return len(p.Submissions) - 1
}

func (p *Pull) IsSameRepoBranch() bool {
	if p.PullSource != nil {
		if p.PullSource.Repo != nil {
			return p.PullSource.Repo == &p.RepoAt
		} else {
			// no repo specified
			return true
		}
	}
	return false
}

func (p *Pull) IsPatch() bool {
	return p.PullSource == nil
}

func (s PullSubmission) AsNiceDiff(targetBranch string) types.NiceDiff {
	patch := s.Patch

	diffs, _, err := gitdiff.Parse(strings.NewReader(patch))
	if err != nil {
		log.Println(err)
	}

	nd := types.NiceDiff{}
	nd.Commit.Parent = targetBranch

	for _, d := range diffs {
		ndiff := types.Diff{}
		ndiff.Name.New = d.NewName
		ndiff.Name.Old = d.OldName
		ndiff.IsBinary = d.IsBinary
		ndiff.IsNew = d.IsNew
		ndiff.IsDelete = d.IsDelete
		ndiff.IsCopy = d.IsCopy
		ndiff.IsRename = d.IsRename

		for _, tf := range d.TextFragments {
			ndiff.TextFragments = append(ndiff.TextFragments, *tf)
			for _, l := range tf.Lines {
				switch l.Op {
				case gitdiff.OpAdd:
					nd.Stat.Insertions += 1
				case gitdiff.OpDelete:
					nd.Stat.Deletions += 1
				}
			}
		}

		nd.Diff = append(nd.Diff, ndiff)
	}

	nd.Stat.FilesChanged = len(diffs)

	return nd
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
	pull.State = PullOpen

	var sourceBranch, sourceRepoAt *string
	if pull.PullSource != nil {
		sourceBranch = &pull.PullSource.Branch
		if pull.PullSource.Repo != nil {
			x := pull.PullSource.Repo.String()
			sourceRepoAt = &x
		}
	}

	_, err = tx.Exec(
		`
		insert into pulls (repo_at, owner_did, pull_id, title, target_branch, body, rkey, state, source_branch, source_repo_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pull.RepoAt,
		pull.OwnerDid,
		pull.PullId,
		pull.Title,
		pull.TargetBranch,
		pull.Body,
		pull.Rkey,
		pull.State,
		sourceBranch,
		sourceRepoAt,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		insert into pull_submissions (pull_id, repo_at, round_number, patch, source_rev)
		values (?, ?, ?, ?, ?)
	`, pull.PullId, pull.RepoAt, 0, pull.Submissions[0].Patch, pull.Submissions[0].SourceRev)
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
			rkey,
			source_branch,
			source_repo_at
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
		var sourceBranch, sourceRepoAt sql.NullString
		err := rows.Scan(
			&pull.OwnerDid,
			&pull.PullId,
			&createdAt,
			&pull.Title,
			&pull.State,
			&pull.TargetBranch,
			&pull.PullAt,
			&pull.Body,
			&pull.Rkey,
			&sourceBranch,
			&sourceRepoAt,
		)
		if err != nil {
			return nil, err
		}

		createdTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		pull.Created = createdTime

		if sourceBranch.Valid {
			pull.PullSource = &PullSource{
				Branch: sourceBranch.String,
			}
			if sourceRepoAt.Valid {
				sourceRepoAtParsed, err := syntax.ParseATURI(sourceRepoAt.String)
				if err != nil {
					return nil, err
				}
				pull.PullSource.Repo = &sourceRepoAtParsed
			}
		}

		pulls = append(pulls, pull)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pulls, nil
}

func GetPull(e Execer, repoAt syntax.ATURI, pullId int) (*Pull, error) {
	query := `
		select
			owner_did,
			pull_id,
			created,
			title,
			state,
			target_branch,
			pull_at,
			repo_at,
			body,
			rkey,
			source_branch,
			source_repo_at
		from
			pulls
		where
			repo_at = ? and pull_id = ?
		`
	row := e.QueryRow(query, repoAt, pullId)

	var pull Pull
	var createdAt string
	var sourceBranch, sourceRepoAt sql.NullString
	err := row.Scan(
		&pull.OwnerDid,
		&pull.PullId,
		&createdAt,
		&pull.Title,
		&pull.State,
		&pull.TargetBranch,
		&pull.PullAt,
		&pull.RepoAt,
		&pull.Body,
		&pull.Rkey,
		&sourceBranch,
		&sourceRepoAt,
	)
	if err != nil {
		return nil, err
	}

	createdTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, err
	}
	pull.Created = createdTime

	// populate source
	if sourceBranch.Valid {
		pull.PullSource = &PullSource{
			Branch: sourceBranch.String,
		}
		if sourceRepoAt.Valid {
			sourceRepoAtParsed, err := syntax.ParseATURI(sourceRepoAt.String)
			if err != nil {
				return nil, err
			}
			pull.PullSource.Repo = &sourceRepoAtParsed
		}
	}

	submissionsQuery := `
		select
			id, pull_id, repo_at, round_number, patch, created, source_rev
		from
			pull_submissions
		where
			repo_at = ? and pull_id = ?
	`
	submissionsRows, err := e.Query(submissionsQuery, repoAt, pullId)
	if err != nil {
		return nil, err
	}
	defer submissionsRows.Close()

	submissionsMap := make(map[int]*PullSubmission)

	for submissionsRows.Next() {
		var submission PullSubmission
		var submissionCreatedStr string
		var submissionSourceRev sql.NullString
		err := submissionsRows.Scan(
			&submission.ID,
			&submission.PullId,
			&submission.RepoAt,
			&submission.RoundNumber,
			&submission.Patch,
			&submissionCreatedStr,
			&submissionSourceRev,
		)
		if err != nil {
			return nil, err
		}

		submissionCreatedTime, err := time.Parse(time.RFC3339, submissionCreatedStr)
		if err != nil {
			return nil, err
		}
		submission.Created = submissionCreatedTime

		if submissionSourceRev.Valid {
			submission.SourceRev = submissionSourceRev.String
		}

		submissionsMap[submission.ID] = &submission
	}
	if err = submissionsRows.Close(); err != nil {
		return nil, err
	}
	if len(submissionsMap) == 0 {
		return &pull, nil
	}

	var args []any
	for k := range submissionsMap {
		args = append(args, k)
	}
	inClause := strings.TrimSuffix(strings.Repeat("?, ", len(submissionsMap)), ", ")
	commentsQuery := fmt.Sprintf(`
		select
			id,
			pull_id,
			submission_id,
			repo_at,
			owner_did,
			comment_at,
			body,
			created
		from
			pull_comments
		where
			submission_id IN (%s)
		order by
			created asc
		`, inClause)
	commentsRows, err := e.Query(commentsQuery, args...)
	if err != nil {
		return nil, err
	}
	defer commentsRows.Close()

	for commentsRows.Next() {
		var comment PullComment
		var commentCreatedStr string
		err := commentsRows.Scan(
			&comment.ID,
			&comment.PullId,
			&comment.SubmissionId,
			&comment.RepoAt,
			&comment.OwnerDid,
			&comment.CommentAt,
			&comment.Body,
			&commentCreatedStr,
		)
		if err != nil {
			return nil, err
		}

		commentCreatedTime, err := time.Parse(time.RFC3339, commentCreatedStr)
		if err != nil {
			return nil, err
		}
		comment.Created = commentCreatedTime

		// Add the comment to its submission
		if submission, ok := submissionsMap[comment.SubmissionId]; ok {
			submission.Comments = append(submission.Comments, comment)
		}

	}
	if err = commentsRows.Err(); err != nil {
		return nil, err
	}

	pull.Submissions = make([]*PullSubmission, len(submissionsMap))
	for _, submission := range submissionsMap {
		pull.Submissions[submission.RoundNumber] = submission
	}

	return &pull, nil
}

func GetPullsByOwnerDid(e Execer, did string) ([]Pull, error) {
	var pulls []Pull

	rows, err := e.Query(`
			select
				owner_did,
				repo_at,
				pull_id,
				created,
				title,
				state
			from
				pulls
			where
				owner_did = ?
			order by
				created desc`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pull Pull
		var createdAt string
		err := rows.Scan(
			&pull.OwnerDid,
			&pull.RepoAt,
			&pull.PullId,
			&createdAt,
			&pull.Title,
			&pull.State,
		)
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

func NewPullComment(e Execer, comment *PullComment) (int64, error) {
	query := `insert into pull_comments (owner_did, repo_at, submission_id, comment_at, pull_id, body) values (?, ?, ?, ?, ?, ?)`
	res, err := e.Exec(
		query,
		comment.OwnerDid,
		comment.RepoAt,
		comment.SubmissionId,
		comment.CommentAt,
		comment.PullId,
		comment.Body,
	)
	if err != nil {
		return 0, err
	}

	i, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	return i, nil
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

func ResubmitPull(e Execer, pull *Pull, newPatch, sourceRev string) error {
	newRoundNumber := len(pull.Submissions)
	_, err := e.Exec(`
		insert into pull_submissions (pull_id, repo_at, round_number, patch, source_rev)
		values (?, ?, ?, ?, ?)
	`, pull.PullId, pull.RepoAt, newRoundNumber, newPatch, sourceRev)

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
			count(case when state = ? then 1 end) as open_count,
			count(case when state = ? then 1 end) as merged_count,
			count(case when state = ? then 1 end) as closed_count
		from pulls
		where repo_at = ?`,
		PullOpen,
		PullMerged,
		PullClosed,
		repoAt,
	)

	var count PullCount
	if err := row.Scan(&count.Open, &count.Merged, &count.Closed); err != nil {
		return PullCount{0, 0, 0}, err
	}

	return count, nil
}
