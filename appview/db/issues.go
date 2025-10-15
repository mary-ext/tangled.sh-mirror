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
	"tangled.org/core/appview/pagination"
)

func PutIssue(tx *sql.Tx, issue *models.Issue) error {
	// ensure sequence exists
	_, err := tx.Exec(`
		insert or ignore into repo_issue_seqs (repo_at, next_issue_id)
		values (?, 1)
	`, issue.RepoAt)
	if err != nil {
		return err
	}

	issues, err := GetIssues(
		tx,
		FilterEq("did", issue.Did),
		FilterEq("rkey", issue.Rkey),
	)
	switch {
	case err != nil:
		return err
	case len(issues) == 0:
		return createNewIssue(tx, issue)
	case len(issues) != 1: // should be unreachable
		return fmt.Errorf("invalid number of issues returned: %d", len(issues))
	default:
		// if content is identical, do not edit
		existingIssue := issues[0]
		if existingIssue.Title == issue.Title && existingIssue.Body == issue.Body {
			return nil
		}

		issue.Id = existingIssue.Id
		issue.IssueId = existingIssue.IssueId
		return updateIssue(tx, issue)
	}
}

func createNewIssue(tx *sql.Tx, issue *models.Issue) error {
	// get next issue_id
	var newIssueId int
	err := tx.QueryRow(`
		update repo_issue_seqs
		set next_issue_id = next_issue_id + 1
		where repo_at = ?
		returning next_issue_id - 1
	`, issue.RepoAt).Scan(&newIssueId)
	if err != nil {
		return err
	}

	// insert new issue
	row := tx.QueryRow(`
		insert into issues (repo_at, did, rkey, issue_id, title, body)
		values (?, ?, ?, ?, ?, ?)
		returning rowid, issue_id
	`, issue.RepoAt, issue.Did, issue.Rkey, newIssueId, issue.Title, issue.Body)

	return row.Scan(&issue.Id, &issue.IssueId)
}

func updateIssue(tx *sql.Tx, issue *models.Issue) error {
	// update existing issue
	_, err := tx.Exec(`
		update issues
		set title = ?, body = ?, edited = ?
		where did = ? and rkey = ?
	`, issue.Title, issue.Body, time.Now().Format(time.RFC3339), issue.Did, issue.Rkey)
	return err
}

func GetIssuesPaginated(e Execer, page pagination.Page, filters ...filter) ([]models.Issue, error) {
	issueMap := make(map[string]*models.Issue) // at-uri -> issue

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

	pLower := FilterGte("row_num", page.Offset+1)
	pUpper := FilterLte("row_num", page.Offset+page.Limit)

	args = append(args, pLower.Arg()...)
	args = append(args, pUpper.Arg()...)
	pagination := " where " + pLower.Condition() + " and " + pUpper.Condition()

	query := fmt.Sprintf(
		`
		select * from (
			select
				id,
				did,
				rkey,
				repo_at,
				issue_id,
				title,
				body,
				open,
				created,
				edited,
				deleted,
				row_number() over (order by created desc) as row_num
			from
				issues
			%s
		) ranked_issues
		%s
		`,
		whereClause,
		pagination,
	)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query issues table: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var issue models.Issue
		var createdAt string
		var editedAt, deletedAt sql.Null[string]
		var rowNum int64
		err := rows.Scan(
			&issue.Id,
			&issue.Did,
			&issue.Rkey,
			&issue.RepoAt,
			&issue.IssueId,
			&issue.Title,
			&issue.Body,
			&issue.Open,
			&createdAt,
			&editedAt,
			&deletedAt,
			&rowNum,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue: %w", err)
		}

		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			issue.Created = t
		}

		if editedAt.Valid {
			if t, err := time.Parse(time.RFC3339, editedAt.V); err == nil {
				issue.Edited = &t
			}
		}

		if deletedAt.Valid {
			if t, err := time.Parse(time.RFC3339, deletedAt.V); err == nil {
				issue.Deleted = &t
			}
		}

		atUri := issue.AtUri().String()
		issueMap[atUri] = &issue
	}

	// collect reverse repos
	repoAts := make([]string, 0, len(issueMap)) // or just []string{}
	for _, issue := range issueMap {
		repoAts = append(repoAts, string(issue.RepoAt))
	}

	repos, err := GetRepos(e, 0, FilterIn("at_uri", repoAts))
	if err != nil {
		return nil, fmt.Errorf("failed to build repo mappings: %w", err)
	}

	repoMap := make(map[string]*models.Repo)
	for i := range repos {
		repoMap[string(repos[i].RepoAt())] = &repos[i]
	}

	for issueAt, i := range issueMap {
		if r, ok := repoMap[string(i.RepoAt)]; ok {
			i.Repo = r
		} else {
			// do not show up the issue if the repo is deleted
			// TODO: foreign key where?
			delete(issueMap, issueAt)
		}
	}

	// collect comments
	issueAts := slices.Collect(maps.Keys(issueMap))

	comments, err := GetIssueComments(e, FilterIn("issue_at", issueAts))
	if err != nil {
		return nil, fmt.Errorf("failed to query comments: %w", err)
	}
	for i := range comments {
		issueAt := comments[i].IssueAt
		if issue, ok := issueMap[issueAt]; ok {
			issue.Comments = append(issue.Comments, comments[i])
		}
	}

	// collect allLabels for each issue
	allLabels, err := GetLabels(e, FilterIn("subject", issueAts))
	if err != nil {
		return nil, fmt.Errorf("failed to query labels: %w", err)
	}
	for issueAt, labels := range allLabels {
		if issue, ok := issueMap[issueAt.String()]; ok {
			issue.Labels = labels
		}
	}

	var issues []models.Issue
	for _, i := range issueMap {
		issues = append(issues, *i)
	}

	sort.Slice(issues, func(i, j int) bool {
		return issues[i].Created.After(issues[j].Created)
	})

	return issues, nil
}

func GetIssues(e Execer, filters ...filter) ([]models.Issue, error) {
	return GetIssuesPaginated(e, pagination.FirstPage(), filters...)
}

func AddIssueComment(e Execer, c models.IssueComment) (int64, error) {
	result, err := e.Exec(
		`insert into issue_comments (
			did,
			rkey,
			issue_at,
			body,
			reply_to,
			created,
			edited
		)
		values (?, ?, ?, ?, ?, ?, null)
		on conflict(did, rkey) do update set
			issue_at = excluded.issue_at,
			body = excluded.body,
			edited = case
				when
					issue_comments.issue_at != excluded.issue_at
					or issue_comments.body != excluded.body
					or issue_comments.reply_to != excluded.reply_to
				then ?
				else issue_comments.edited
			end`,
		c.Did,
		c.Rkey,
		c.IssueAt,
		c.Body,
		c.ReplyTo,
		c.Created.Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	return id, nil
}

func DeleteIssueComments(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`update issue_comments set body = "", deleted = strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now') %s`, whereClause)

	_, err := e.Exec(query, args...)
	return err
}

func GetIssueComments(e Execer, filters ...filter) ([]models.IssueComment, error) {
	var comments []models.IssueComment

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
			did,
			rkey,
			issue_at,
			reply_to,
			body,
			created,
			edited,
			deleted
		from
			issue_comments
		%s
		`, whereClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var comment models.IssueComment
		var created string
		var rkey, edited, deleted, replyTo sql.Null[string]
		err := rows.Scan(
			&comment.Id,
			&comment.Did,
			&rkey,
			&comment.IssueAt,
			&replyTo,
			&comment.Body,
			&created,
			&edited,
			&deleted,
		)
		if err != nil {
			return nil, err
		}

		// this is a remnant from old times, newer comments always have rkey
		if rkey.Valid {
			comment.Rkey = rkey.V
		}

		if t, err := time.Parse(time.RFC3339, created); err == nil {
			comment.Created = t
		}

		if edited.Valid {
			if t, err := time.Parse(time.RFC3339, edited.V); err == nil {
				comment.Edited = &t
			}
		}

		if deleted.Valid {
			if t, err := time.Parse(time.RFC3339, deleted.V); err == nil {
				comment.Deleted = &t
			}
		}

		if replyTo.Valid {
			comment.ReplyTo = &replyTo.V
		}

		comments = append(comments, comment)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return comments, nil
}

func DeleteIssues(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`delete from issues %s`, whereClause)
	_, err := e.Exec(query, args...)
	return err
}

func CloseIssues(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`update issues set open = 0 %s`, whereClause)
	_, err := e.Exec(query, args...)
	return err
}

func ReopenIssues(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`update issues set open = 1 %s`, whereClause)
	_, err := e.Exec(query, args...)
	return err
}

func GetIssueCount(e Execer, repoAt syntax.ATURI) (models.IssueCount, error) {
	row := e.QueryRow(`
		select
			count(case when open = 1 then 1 end) as open_count,
			count(case when open = 0 then 1 end) as closed_count
		from issues
		where repo_at = ?`,
		repoAt,
	)

	var count models.IssueCount
	if err := row.Scan(&count.Open, &count.Closed); err != nil {
		return models.IssueCount{}, err
	}

	return count, nil
}
