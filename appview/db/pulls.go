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
	"tangled.org/core/appview/models"
)

func NewPull(tx *sql.Tx, pull *models.Pull) error {
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
	pull.State = models.PullOpen

	var sourceBranch, sourceRepoAt *string
	if pull.PullSource != nil {
		sourceBranch = &pull.PullSource.Branch
		if pull.PullSource.RepoAt != nil {
			x := pull.PullSource.RepoAt.String()
			sourceRepoAt = &x
		}
	}

	var stackId, changeId, parentChangeId *string
	if pull.StackId != "" {
		stackId = &pull.StackId
	}
	if pull.ChangeId != "" {
		changeId = &pull.ChangeId
	}
	if pull.ParentChangeId != "" {
		parentChangeId = &pull.ParentChangeId
	}

	result, err := tx.Exec(
		`
		insert into pulls (
			repo_at, owner_did, pull_id, title, target_branch, body, rkey, state, source_branch, source_repo_at, stack_id, change_id, parent_change_id
		)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		stackId,
		changeId,
		parentChangeId,
	)
	if err != nil {
		return err
	}

	// Set the database primary key ID
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	pull.ID = int(id)

	_, err = tx.Exec(`
		insert into pull_submissions (pull_at, round_number, patch, source_rev)
		values (?, ?, ?, ?)
	`, pull.PullAt(), 0, pull.Submissions[0].Patch, pull.Submissions[0].SourceRev)
	return err
}

func GetPullAt(e Execer, repoAt syntax.ATURI, pullId int) (syntax.ATURI, error) {
	pull, err := GetPull(e, repoAt, pullId)
	if err != nil {
		return "", err
	}
	return pull.PullAt(), err
}

func NextPullId(e Execer, repoAt syntax.ATURI) (int, error) {
	var pullId int
	err := e.QueryRow(`select next_pull_id from repo_pull_seqs where repo_at = ?`, repoAt).Scan(&pullId)
	return pullId - 1, err
}

func GetPullsWithLimit(e Execer, limit int, filters ...filter) ([]*models.Pull, error) {
	pulls := make(map[syntax.ATURI]*models.Pull)

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

	query := fmt.Sprintf(`
		select
			id,
			owner_did,
			repo_at,
			pull_id,
			created,
			title,
			state,
			target_branch,
			body,
			rkey,
			source_branch,
			source_repo_at,
			stack_id,
			change_id,
			parent_change_id
		from
			pulls
		%s
		order by
			created desc
		%s
	`, whereClause, limitClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pull models.Pull
		var createdAt string
		var sourceBranch, sourceRepoAt, stackId, changeId, parentChangeId sql.NullString
		err := rows.Scan(
			&pull.ID,
			&pull.OwnerDid,
			&pull.RepoAt,
			&pull.PullId,
			&createdAt,
			&pull.Title,
			&pull.State,
			&pull.TargetBranch,
			&pull.Body,
			&pull.Rkey,
			&sourceBranch,
			&sourceRepoAt,
			&stackId,
			&changeId,
			&parentChangeId,
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
			pull.PullSource = &models.PullSource{
				Branch: sourceBranch.String,
			}
			if sourceRepoAt.Valid {
				sourceRepoAtParsed, err := syntax.ParseATURI(sourceRepoAt.String)
				if err != nil {
					return nil, err
				}
				pull.PullSource.RepoAt = &sourceRepoAtParsed
			}
		}

		if stackId.Valid {
			pull.StackId = stackId.String
		}
		if changeId.Valid {
			pull.ChangeId = changeId.String
		}
		if parentChangeId.Valid {
			pull.ParentChangeId = parentChangeId.String
		}

		pulls[pull.PullAt()] = &pull
	}

	var pullAts []syntax.ATURI
	for _, p := range pulls {
		pullAts = append(pullAts, p.PullAt())
	}
	submissionsMap, err := GetPullSubmissions(e, FilterIn("pull_at", pullAts))
	if err != nil {
		return nil, fmt.Errorf("failed to get submissions: %w", err)
	}

	for pullAt, submissions := range submissionsMap {
		if p, ok := pulls[pullAt]; ok {
			p.Submissions = submissions
		}
	}

	orderedByPullId := []*models.Pull{}
	for _, p := range pulls {
		orderedByPullId = append(orderedByPullId, p)
	}
	sort.Slice(orderedByPullId, func(i, j int) bool {
		return orderedByPullId[i].PullId > orderedByPullId[j].PullId
	})

	return orderedByPullId, nil
}

func GetPulls(e Execer, filters ...filter) ([]*models.Pull, error) {
	return GetPullsWithLimit(e, 0, filters...)
}

func GetPull(e Execer, repoAt syntax.ATURI, pullId int) (*models.Pull, error) {
	pulls, err := GetPullsWithLimit(e, 1, FilterEq("repo_at", repoAt), FilterEq("pull_id", pullId))
	if err != nil {
		return nil, err
	}
	if pulls == nil {
		return nil, sql.ErrNoRows
	}

	return pulls[0], nil
}

// mapping from pull -> pull submissions
func GetPullSubmissions(e Execer, filters ...filter) (map[syntax.ATURI][]*models.PullSubmission, error) {
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

	query := fmt.Sprintf(`
		select
			id,
			pull_at,
			round_number,
			patch,
			created,
			source_rev
		from
			pull_submissions
		%s
		order by
			round_number asc
		`, whereClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	submissionMap := make(map[int]*models.PullSubmission)

	for rows.Next() {
		var submission models.PullSubmission
		var createdAt string
		var sourceRev sql.NullString
		err := rows.Scan(
			&submission.ID,
			&submission.PullAt,
			&submission.RoundNumber,
			&submission.Patch,
			&createdAt,
			&sourceRev,
		)
		if err != nil {
			return nil, err
		}

		createdTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		submission.Created = createdTime

		if sourceRev.Valid {
			submission.SourceRev = sourceRev.String
		}

		submissionMap[submission.ID] = &submission
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Get comments for all submissions using GetPullComments
	submissionIds := slices.Collect(maps.Keys(submissionMap))
	comments, err := GetPullComments(e, FilterIn("submission_id", submissionIds))
	if err != nil {
		return nil, err
	}
	for _, comment := range comments {
		if submission, ok := submissionMap[comment.SubmissionId]; ok {
			submission.Comments = append(submission.Comments, comment)
		}
	}

	// order the submissions by pull_at
	m := make(map[syntax.ATURI][]*models.PullSubmission)
	for _, s := range submissionMap {
		m[s.PullAt] = append(m[s.PullAt], s)
	}

	return m, nil
}

func GetPullComments(e Execer, filters ...filter) ([]models.PullComment, error) {
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

	query := fmt.Sprintf(`
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
		%s
		order by
			created asc
		`, whereClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []models.PullComment
	for rows.Next() {
		var comment models.PullComment
		var createdAt string
		err := rows.Scan(
			&comment.ID,
			&comment.PullId,
			&comment.SubmissionId,
			&comment.RepoAt,
			&comment.OwnerDid,
			&comment.CommentAt,
			&comment.Body,
			&createdAt,
		)
		if err != nil {
			return nil, err
		}

		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			comment.Created = t
		}

		comments = append(comments, comment)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return comments, nil
}

// timeframe here is directly passed into the sql query filter, and any
// timeframe in the past should be negative; e.g.: "-3 months"
func GetPullsByOwnerDid(e Execer, did, timeframe string) ([]models.Pull, error) {
	var pulls []models.Pull

	rows, err := e.Query(`
			select
				p.owner_did,
				p.repo_at,
				p.pull_id,
				p.created,
				p.title,
				p.state,
				r.did,
				r.name,
				r.knot,
				r.rkey,
				r.created
			from
				pulls p
			join
				repos r on p.repo_at = r.at_uri
			where
				p.owner_did = ? and p.created >= date ('now', ?)
			order by
				p.created desc`, did, timeframe)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pull models.Pull
		var repo models.Repo
		var pullCreatedAt, repoCreatedAt string
		err := rows.Scan(
			&pull.OwnerDid,
			&pull.RepoAt,
			&pull.PullId,
			&pullCreatedAt,
			&pull.Title,
			&pull.State,
			&repo.Did,
			&repo.Name,
			&repo.Knot,
			&repo.Rkey,
			&repoCreatedAt,
		)
		if err != nil {
			return nil, err
		}

		pullCreatedTime, err := time.Parse(time.RFC3339, pullCreatedAt)
		if err != nil {
			return nil, err
		}
		pull.Created = pullCreatedTime

		repoCreatedTime, err := time.Parse(time.RFC3339, repoCreatedAt)
		if err != nil {
			return nil, err
		}
		repo.Created = repoCreatedTime

		pull.Repo = &repo

		pulls = append(pulls, pull)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pulls, nil
}

func NewPullComment(e Execer, comment *models.PullComment) (int64, error) {
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

func SetPullState(e Execer, repoAt syntax.ATURI, pullId int, pullState models.PullState) error {
	_, err := e.Exec(
		`update pulls set state = ? where repo_at = ? and pull_id = ? and (state <> ? or state <> ?)`,
		pullState,
		repoAt,
		pullId,
		models.PullDeleted, // only update state of non-deleted pulls
		models.PullMerged,  // only update state of non-merged pulls
	)
	return err
}

func ClosePull(e Execer, repoAt syntax.ATURI, pullId int) error {
	err := SetPullState(e, repoAt, pullId, models.PullClosed)
	return err
}

func ReopenPull(e Execer, repoAt syntax.ATURI, pullId int) error {
	err := SetPullState(e, repoAt, pullId, models.PullOpen)
	return err
}

func MergePull(e Execer, repoAt syntax.ATURI, pullId int) error {
	err := SetPullState(e, repoAt, pullId, models.PullMerged)
	return err
}

func DeletePull(e Execer, repoAt syntax.ATURI, pullId int) error {
	err := SetPullState(e, repoAt, pullId, models.PullDeleted)
	return err
}

func ResubmitPull(e Execer, pull *models.Pull, newPatch, sourceRev string) error {
	newRoundNumber := len(pull.Submissions)
	_, err := e.Exec(`
		insert into pull_submissions (pull_at, round_number, patch, source_rev)
		values (?, ?, ?, ?)
	`, pull.PullAt(), newRoundNumber, newPatch, sourceRev)

	return err
}

func SetPullParentChangeId(e Execer, parentChangeId string, filters ...filter) error {
	var conditions []string
	var args []any

	args = append(args, parentChangeId)

	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf("update pulls set parent_change_id = ? %s", whereClause)
	_, err := e.Exec(query, args...)

	return err
}

// Only used when stacking to update contents in the event of a rebase (the interdiff should be empty).
// otherwise submissions are immutable
func UpdatePull(e Execer, newPatch, sourceRev string, filters ...filter) error {
	var conditions []string
	var args []any

	args = append(args, sourceRev)
	args = append(args, newPatch)

	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf("update pull_submissions set source_rev = ?, patch = ? %s", whereClause)
	_, err := e.Exec(query, args...)

	return err
}

func GetPullCount(e Execer, repoAt syntax.ATURI) (models.PullCount, error) {
	row := e.QueryRow(`
		select
			count(case when state = ? then 1 end) as open_count,
			count(case when state = ? then 1 end) as merged_count,
			count(case when state = ? then 1 end) as closed_count,
			count(case when state = ? then 1 end) as deleted_count
		from pulls
		where repo_at = ?`,
		models.PullOpen,
		models.PullMerged,
		models.PullClosed,
		models.PullDeleted,
		repoAt,
	)

	var count models.PullCount
	if err := row.Scan(&count.Open, &count.Merged, &count.Closed, &count.Deleted); err != nil {
		return models.PullCount{Open: 0, Merged: 0, Closed: 0, Deleted: 0}, err
	}

	return count, nil
}

//	change-id     parent-change-id
//
// 4       w      ,-------- z          (TOP)
// 3       z <----',------- y
// 2       y <-----',------ x
// 1       x <------'      nil         (BOT)
//
// `w` is parent of none, so it is the top of the stack
func GetStack(e Execer, stackId string) (models.Stack, error) {
	unorderedPulls, err := GetPulls(
		e,
		FilterEq("stack_id", stackId),
		FilterNotEq("state", models.PullDeleted),
	)
	if err != nil {
		return nil, err
	}
	// map of parent-change-id to pull
	changeIdMap := make(map[string]*models.Pull, len(unorderedPulls))
	parentMap := make(map[string]*models.Pull, len(unorderedPulls))
	for _, p := range unorderedPulls {
		changeIdMap[p.ChangeId] = p
		if p.ParentChangeId != "" {
			parentMap[p.ParentChangeId] = p
		}
	}

	// the top of the stack is the pull that is not a parent of any pull
	var topPull *models.Pull
	for _, maybeTop := range unorderedPulls {
		if _, ok := parentMap[maybeTop.ChangeId]; !ok {
			topPull = maybeTop
			break
		}
	}

	pulls := []*models.Pull{}
	for {
		pulls = append(pulls, topPull)
		if topPull.ParentChangeId != "" {
			if next, ok := changeIdMap[topPull.ParentChangeId]; ok {
				topPull = next
			} else {
				return nil, fmt.Errorf("failed to find parent pull request, stack is malformed")
			}
		} else {
			break
		}
	}

	return pulls, nil
}

func GetAbandonedPulls(e Execer, stackId string) ([]*models.Pull, error) {
	pulls, err := GetPulls(
		e,
		FilterEq("stack_id", stackId),
		FilterEq("state", models.PullDeleted),
	)
	if err != nil {
		return nil, err
	}

	return pulls, nil
}
