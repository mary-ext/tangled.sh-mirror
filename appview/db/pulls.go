package db

import (
	"database/sql"
	"fmt"
	"log"
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
		insert into pull_submissions (pull_id, repo_at, round_number, patch, source_rev)
		values (?, ?, ?, ?, ?)
	`, pull.PullId, pull.RepoAt, 0, pull.Submissions[0].Patch, pull.Submissions[0].SourceRev)
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
	pulls := make(map[int]*models.Pull)

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

		pulls[pull.PullId] = &pull
	}

	// get latest round no. for each pull
	inClause := strings.TrimSuffix(strings.Repeat("?, ", len(pulls)), ", ")
	submissionsQuery := fmt.Sprintf(`
		select
			id, pull_id, round_number, patch, created, source_rev
		from
			pull_submissions
		where
			repo_at in (%s) and pull_id in (%s)
	`, inClause, inClause)

	args = make([]any, len(pulls)*2)
	idx := 0
	for _, p := range pulls {
		args[idx] = p.RepoAt
		idx += 1
	}
	for _, p := range pulls {
		args[idx] = p.PullId
		idx += 1
	}
	submissionsRows, err := e.Query(submissionsQuery, args...)
	if err != nil {
		return nil, err
	}
	defer submissionsRows.Close()

	for submissionsRows.Next() {
		var s models.PullSubmission
		var sourceRev sql.NullString
		var createdAt string
		err := submissionsRows.Scan(
			&s.ID,
			&s.PullId,
			&s.RoundNumber,
			&s.Patch,
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
		s.Created = createdTime

		if sourceRev.Valid {
			s.SourceRev = sourceRev.String
		}

		if p, ok := pulls[s.PullId]; ok {
			p.Submissions = make([]*models.PullSubmission, s.RoundNumber+1)
			p.Submissions[s.RoundNumber] = &s
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// get comment count on latest submission on each pull
	inClause = strings.TrimSuffix(strings.Repeat("?, ", len(pulls)), ", ")
	commentsQuery := fmt.Sprintf(`
		select
			count(id), pull_id
		from
			pull_comments
		where
			submission_id in (%s)
		group by
			submission_id
	`, inClause)

	args = []any{}
	for _, p := range pulls {
		args = append(args, p.Submissions[p.LastRoundNumber()].ID)
	}
	commentsRows, err := e.Query(commentsQuery, args...)
	if err != nil {
		return nil, err
	}
	defer commentsRows.Close()

	for commentsRows.Next() {
		var commentCount, pullId int
		err := commentsRows.Scan(
			&commentCount,
			&pullId,
		)
		if err != nil {
			return nil, err
		}
		if p, ok := pulls[pullId]; ok {
			p.Submissions[p.LastRoundNumber()].Comments = make([]models.PullComment, commentCount)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	query := `
		select
			id,
			owner_did,
			pull_id,
			created,
			title,
			state,
			target_branch,
			repo_at,
			body,
			rkey,
			source_branch,
			source_repo_at,
			stack_id,
			change_id,
			parent_change_id
		from
			pulls
		where
			repo_at = ? and pull_id = ?
		`
	row := e.QueryRow(query, repoAt, pullId)

	var pull models.Pull
	var createdAt string
	var sourceBranch, sourceRepoAt, stackId, changeId, parentChangeId sql.NullString
	err := row.Scan(
		&pull.ID,
		&pull.OwnerDid,
		&pull.PullId,
		&createdAt,
		&pull.Title,
		&pull.State,
		&pull.TargetBranch,
		&pull.RepoAt,
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

	// populate source
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

	submissionsMap := make(map[int]*models.PullSubmission)

	for submissionsRows.Next() {
		var submission models.PullSubmission
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
		var comment models.PullComment
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

	var pullSourceRepo *models.Repo
	if pull.PullSource != nil {
		if pull.PullSource.RepoAt != nil {
			pullSourceRepo, err = GetRepoByAtUri(e, pull.PullSource.RepoAt.String())
			if err != nil {
				log.Printf("failed to get repo by at uri: %v", err)
			} else {
				pull.PullSource.Repo = pullSourceRepo
			}
		}
	}

	pull.Submissions = make([]*models.PullSubmission, len(submissionsMap))
	for _, submission := range submissionsMap {
		pull.Submissions[submission.RoundNumber] = submission
	}

	return &pull, nil
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
		insert into pull_submissions (pull_id, repo_at, round_number, patch, source_rev)
		values (?, ?, ?, ?, ?)
	`, pull.PullId, pull.RepoAt, newRoundNumber, newPatch, sourceRev)

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
