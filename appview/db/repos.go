package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/models"
)

func GetRepos(e Execer, limit int, filters ...filter) ([]models.Repo, error) {
	repoMap := make(map[syntax.ATURI]*models.Repo)

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
		limitClause = fmt.Sprintf(" limit %d", limit)
	}

	repoQuery := fmt.Sprintf(
		`select
			id,
			did,
			name,
			knot,
			rkey,
			created,
			description,
			website,
			topics,
			source,
			spindle
		from
			repos r
		%s
		order by created desc
		%s`,
		whereClause,
		limitClause,
	)
	rows, err := e.Query(repoQuery, args...)

	if err != nil {
		return nil, fmt.Errorf("failed to execute repo query: %w ", err)
	}

	for rows.Next() {
		var repo models.Repo
		var createdAt string
		var description, website, topicStr, source, spindle sql.NullString

		err := rows.Scan(
			&repo.Id,
			&repo.Did,
			&repo.Name,
			&repo.Knot,
			&repo.Rkey,
			&createdAt,
			&description,
			&website,
			&topicStr,
			&source,
			&spindle,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to execute repo query: %w ", err)
		}

		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			repo.Created = t
		}
		if description.Valid {
			repo.Description = description.String
		}
		if website.Valid {
			repo.Website = website.String
		}
		if topicStr.Valid {
			repo.Topics = strings.Fields(topicStr.String)
		}
		if source.Valid {
			repo.Source = source.String
		}
		if spindle.Valid {
			repo.Spindle = spindle.String
		}

		repo.RepoStats = &models.RepoStats{}
		repoMap[repo.RepoAt()] = &repo
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to execute repo query: %w ", err)
	}

	inClause := strings.TrimSuffix(strings.Repeat("?, ", len(repoMap)), ", ")
	args = make([]any, len(repoMap))

	i := 0
	for _, r := range repoMap {
		args[i] = r.RepoAt()
		i++
	}

	// Get labels for all repos
	labelsQuery := fmt.Sprintf(
		`select repo_at, label_at from repo_labels where repo_at in (%s)`,
		inClause,
	)
	rows, err = e.Query(labelsQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute labels query: %w ", err)
	}
	for rows.Next() {
		var repoat, labelat string
		if err := rows.Scan(&repoat, &labelat); err != nil {
			log.Println("err", "err", err)
			continue
		}
		if r, ok := repoMap[syntax.ATURI(repoat)]; ok {
			r.Labels = append(r.Labels, labelat)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to execute labels query: %w ", err)
	}

	languageQuery := fmt.Sprintf(
		`
		select repo_at, language
		from (
			select
			repo_at,
			language,
			row_number() over (
				partition by repo_at
				order by bytes desc
			) as rn
			from repo_languages
			where repo_at in (%s)
			and is_default_ref = 1
		)
		where rn = 1
		`,
		inClause,
	)
	rows, err = e.Query(languageQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute lang query: %w ", err)
	}
	for rows.Next() {
		var repoat, lang string
		if err := rows.Scan(&repoat, &lang); err != nil {
			log.Println("err", "err", err)
			continue
		}
		if r, ok := repoMap[syntax.ATURI(repoat)]; ok {
			r.RepoStats.Language = lang
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to execute lang query: %w ", err)
	}

	starCountQuery := fmt.Sprintf(
		`select
			repo_at, count(1)
		from stars
		where repo_at in (%s)
		group by repo_at`,
		inClause,
	)
	rows, err = e.Query(starCountQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute star-count query: %w ", err)
	}
	for rows.Next() {
		var repoat string
		var count int
		if err := rows.Scan(&repoat, &count); err != nil {
			log.Println("err", "err", err)
			continue
		}
		if r, ok := repoMap[syntax.ATURI(repoat)]; ok {
			r.RepoStats.StarCount = count
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to execute star-count query: %w ", err)
	}

	issueCountQuery := fmt.Sprintf(
		`select
			repo_at,
			count(case when open = 1 then 1 end) as open_count,
			count(case when open = 0 then 1 end) as closed_count
		from issues
		where repo_at in (%s)
		group by repo_at`,
		inClause,
	)
	rows, err = e.Query(issueCountQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute issue-count query: %w ", err)
	}
	for rows.Next() {
		var repoat string
		var open, closed int
		if err := rows.Scan(&repoat, &open, &closed); err != nil {
			log.Println("err", "err", err)
			continue
		}
		if r, ok := repoMap[syntax.ATURI(repoat)]; ok {
			r.RepoStats.IssueCount.Open = open
			r.RepoStats.IssueCount.Closed = closed
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to execute issue-count query: %w ", err)
	}

	pullCountQuery := fmt.Sprintf(
		`select
			repo_at,
			count(case when state = ? then 1 end) as open_count,
			count(case when state = ? then 1 end) as merged_count,
			count(case when state = ? then 1 end) as closed_count,
			count(case when state = ? then 1 end) as deleted_count
		from pulls
		where repo_at in (%s)
		group by repo_at`,
		inClause,
	)
	args = append([]any{
		models.PullOpen,
		models.PullMerged,
		models.PullClosed,
		models.PullDeleted,
	}, args...)
	rows, err = e.Query(
		pullCountQuery,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to execute pulls-count query: %w ", err)
	}
	for rows.Next() {
		var repoat string
		var open, merged, closed, deleted int
		if err := rows.Scan(&repoat, &open, &merged, &closed, &deleted); err != nil {
			log.Println("err", "err", err)
			continue
		}
		if r, ok := repoMap[syntax.ATURI(repoat)]; ok {
			r.RepoStats.PullCount.Open = open
			r.RepoStats.PullCount.Merged = merged
			r.RepoStats.PullCount.Closed = closed
			r.RepoStats.PullCount.Deleted = deleted
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to execute pulls-count query: %w ", err)
	}

	var repos []models.Repo
	for _, r := range repoMap {
		repos = append(repos, *r)
	}

	slices.SortFunc(repos, func(a, b models.Repo) int {
		if a.Created.After(b.Created) {
			return -1
		}
		return 1
	})

	return repos, nil
}

// helper to get exactly one repo
func GetRepo(e Execer, filters ...filter) (*models.Repo, error) {
	repos, err := GetRepos(e, 0, filters...)
	if err != nil {
		return nil, err
	}

	if repos == nil {
		return nil, sql.ErrNoRows
	}

	if len(repos) != 1 {
		return nil, fmt.Errorf("too many rows returned")
	}

	return &repos[0], nil
}

func CountRepos(e Execer, filters ...filter) (int64, error) {
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

	repoQuery := fmt.Sprintf(`select count(1) from repos %s`, whereClause)
	var count int64
	err := e.QueryRow(repoQuery, args...).Scan(&count)

	if !errors.Is(err, sql.ErrNoRows) && err != nil {
		return 0, err
	}

	return count, nil
}

func GetRepoByAtUri(e Execer, atUri string) (*models.Repo, error) {
	var repo models.Repo
	var nullableDescription sql.NullString
	var nullableWebsite sql.NullString
	var nullableTopicStr sql.NullString

	row := e.QueryRow(`select id, did, name, knot, created, rkey, description, website, topics from repos where at_uri = ?`, atUri)

	var createdAt string
	if err := row.Scan(&repo.Id, &repo.Did, &repo.Name, &repo.Knot, &createdAt, &repo.Rkey, &nullableDescription, &nullableWebsite, &nullableTopicStr); err != nil {
		return nil, err
	}
	createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
	repo.Created = createdAtTime

	if nullableDescription.Valid {
		repo.Description = nullableDescription.String
	}
	if nullableWebsite.Valid {
		repo.Website = nullableWebsite.String
	}
	if nullableTopicStr.Valid {
		repo.Topics = strings.Fields(nullableTopicStr.String)
	}

	return &repo, nil
}

func PutRepo(tx *sql.Tx, repo models.Repo) error {
	_, err := tx.Exec(
		`update repos
		set knot = ?, description = ?, website = ?, topics = ?
		where did = ? and rkey = ?
		`,
		repo.Knot, repo.Description, repo.Website, repo.TopicStr(), repo.Did, repo.Rkey,
	)
	return err
}

func AddRepo(tx *sql.Tx, repo *models.Repo) error {
	_, err := tx.Exec(
		`insert into repos
		(did, name, knot, rkey, at_uri, description, website, topics, source)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		repo.Did, repo.Name, repo.Knot, repo.Rkey, repo.RepoAt().String(), repo.Description, repo.Website, repo.TopicStr(), repo.Source,
	)
	if err != nil {
		return fmt.Errorf("failed to insert repo: %w", err)
	}

	for _, dl := range repo.Labels {
		if err := SubscribeLabel(tx, &models.RepoLabel{
			RepoAt:  repo.RepoAt(),
			LabelAt: syntax.ATURI(dl),
		}); err != nil {
			return fmt.Errorf("failed to subscribe to label: %w", err)
		}
	}

	return nil
}

func RemoveRepo(e Execer, did, name string) error {
	_, err := e.Exec(`delete from repos where did = ? and name = ?`, did, name)
	return err
}

func GetRepoSource(e Execer, repoAt syntax.ATURI) (string, error) {
	var nullableSource sql.NullString
	err := e.QueryRow(`select source from repos where at_uri = ?`, repoAt).Scan(&nullableSource)
	if err != nil {
		return "", err
	}
	return nullableSource.String, nil
}

func GetForksByDid(e Execer, did string) ([]models.Repo, error) {
	var repos []models.Repo

	rows, err := e.Query(
		`select distinct r.id, r.did, r.name, r.knot, r.rkey, r.description, r.website, r.created, r.source
		from repos r
		left join collaborators c on r.at_uri = c.repo_at
		where (r.did = ? or c.subject_did = ?)
			and r.source is not null
			and r.source != ''
		order by r.created desc`,
		did, did,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo models.Repo
		var createdAt string
		var nullableDescription sql.NullString
		var nullableWebsite sql.NullString
		var nullableSource sql.NullString

		err := rows.Scan(&repo.Id, &repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &nullableWebsite, &createdAt, &nullableSource)
		if err != nil {
			return nil, err
		}

		if nullableDescription.Valid {
			repo.Description = nullableDescription.String
		}

		if nullableSource.Valid {
			repo.Source = nullableSource.String
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			repo.Created = time.Now()
		} else {
			repo.Created = createdAtTime
		}

		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return repos, nil
}

func GetForkByDid(e Execer, did string, name string) (*models.Repo, error) {
	var repo models.Repo
	var createdAt string
	var nullableDescription sql.NullString
	var nullableWebsite sql.NullString
	var nullableTopicStr sql.NullString
	var nullableSource sql.NullString

	row := e.QueryRow(
		`select id, did, name, knot, rkey, description, website, topics, created, source
		from repos
		where did = ? and name = ? and source is not null and source != ''`,
		did, name,
	)

	err := row.Scan(&repo.Id, &repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &nullableWebsite, &nullableTopicStr, &createdAt, &nullableSource)
	if err != nil {
		return nil, err
	}

	if nullableDescription.Valid {
		repo.Description = nullableDescription.String
	}

	if nullableWebsite.Valid {
		repo.Website = nullableWebsite.String
	}

	if nullableTopicStr.Valid {
		repo.Topics = strings.Fields(nullableTopicStr.String)
	}

	if nullableSource.Valid {
		repo.Source = nullableSource.String
	}

	createdAtTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		repo.Created = time.Now()
	} else {
		repo.Created = createdAtTime
	}

	return &repo, nil
}

func UpdateDescription(e Execer, repoAt, newDescription string) error {
	_, err := e.Exec(
		`update repos set description = ? where at_uri = ?`, newDescription, repoAt)
	return err
}

func UpdateSpindle(e Execer, repoAt string, spindle *string) error {
	_, err := e.Exec(
		`update repos set spindle = ? where at_uri = ?`, spindle, repoAt)
	return err
}

func SubscribeLabel(e Execer, rl *models.RepoLabel) error {
	query := `insert or ignore into repo_labels (repo_at, label_at) values (?, ?)`

	_, err := e.Exec(query, rl.RepoAt.String(), rl.LabelAt.String())
	return err
}

func UnsubscribeLabel(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`delete from repo_labels %s`, whereClause)
	_, err := e.Exec(query, args...)
	return err
}

func GetRepoLabels(e Execer, filters ...filter) ([]models.RepoLabel, error) {
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

	query := fmt.Sprintf(`select id, repo_at, label_at from repo_labels %s`, whereClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var labels []models.RepoLabel
	for rows.Next() {
		var label models.RepoLabel

		err := rows.Scan(&label.Id, &label.RepoAt, &label.LabelAt)
		if err != nil {
			return nil, err
		}

		labels = append(labels, label)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return labels, nil
}
