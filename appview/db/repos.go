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
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
)

type Repo struct {
	Did         string
	Name        string
	Knot        string
	Rkey        string
	Created     time.Time
	Description string
	Spindle     string
	Labels      []string

	// optionally, populate this when querying for reverse mappings
	RepoStats *RepoStats

	// optional
	Source string
}

func (r *Repo) AsRecord() tangled.Repo {
	var source, spindle, description *string

	if r.Source != "" {
		source = &r.Source
	}

	if r.Spindle != "" {
		spindle = &r.Spindle
	}

	if r.Description != "" {
		description = &r.Description
	}

	return tangled.Repo{
		Knot:        r.Knot,
		Name:        r.Name,
		Description: description,
		CreatedAt:   r.Created.Format(time.RFC3339),
		Source:      source,
		Spindle:     spindle,
		Labels:      r.Labels,
	}
}

func (r Repo) RepoAt() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", r.Did, tangled.RepoNSID, r.Rkey))
}

func (r Repo) DidSlashRepo() string {
	p, _ := securejoin.SecureJoin(r.Did, r.Name)
	return p
}

func GetRepos(e Execer, limit int, filters ...filter) ([]Repo, error) {
	repoMap := make(map[syntax.ATURI]*Repo)

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
			did,
			name,
			knot,
			rkey,
			created,
			description,
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
		var repo Repo
		var createdAt string
		var description, source, spindle sql.NullString

		err := rows.Scan(
			&repo.Did,
			&repo.Name,
			&repo.Knot,
			&repo.Rkey,
			&createdAt,
			&description,
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
		if source.Valid {
			repo.Source = source.String
		}
		if spindle.Valid {
			repo.Spindle = spindle.String
		}

		repo.RepoStats = &RepoStats{}
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
		select
			repo_at, language
		from
			repo_languages r1
		where
			repo_at IN (%s)
			and is_default_ref = 1
			and id = (
				select id
					from repo_languages r2
					where r2.repo_at = r1.repo_at
					and r2.is_default_ref = 1
					order by bytes desc
					limit 1
			);
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
		PullOpen,
		PullMerged,
		PullClosed,
		PullDeleted,
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

	var repos []Repo
	for _, r := range repoMap {
		repos = append(repos, *r)
	}

	slices.SortFunc(repos, func(a, b Repo) int {
		if a.Created.After(b.Created) {
			return -1
		}
		return 1
	})

	return repos, nil
}

// helper to get exactly one repo
func GetRepo(e Execer, filters ...filter) (*Repo, error) {
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

func GetRepoByAtUri(e Execer, atUri string) (*Repo, error) {
	var repo Repo
	var nullableDescription sql.NullString

	row := e.QueryRow(`select did, name, knot, created, rkey, description from repos where at_uri = ?`, atUri)

	var createdAt string
	if err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &createdAt, &repo.Rkey, &nullableDescription); err != nil {
		return nil, err
	}
	createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
	repo.Created = createdAtTime

	if nullableDescription.Valid {
		repo.Description = nullableDescription.String
	} else {
		repo.Description = ""
	}

	return &repo, nil
}

func AddRepo(e Execer, repo *Repo) error {
	_, err := e.Exec(
		`insert into repos
		(did, name, knot, rkey, at_uri, description, source)
		values (?, ?, ?, ?, ?, ?, ?)`,
		repo.Did, repo.Name, repo.Knot, repo.Rkey, repo.RepoAt().String(), repo.Description, repo.Source,
	)
	return err
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

func GetForksByDid(e Execer, did string) ([]Repo, error) {
	var repos []Repo

	rows, err := e.Query(
		`select distinct r.did, r.name, r.knot, r.rkey, r.description, r.created, r.source
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
		var repo Repo
		var createdAt string
		var nullableDescription sql.NullString
		var nullableSource sql.NullString

		err := rows.Scan(&repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &createdAt, &nullableSource)
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

func GetForkByDid(e Execer, did string, name string) (*Repo, error) {
	var repo Repo
	var createdAt string
	var nullableDescription sql.NullString
	var nullableSource sql.NullString

	row := e.QueryRow(
		`select did, name, knot, rkey, description, created, source
		from repos
		where did = ? and name = ? and source is not null and source != ''`,
		did, name,
	)

	err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &createdAt, &nullableSource)
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

type RepoStats struct {
	Language   string
	StarCount  int
	IssueCount IssueCount
	PullCount  PullCount
}

type RepoLabel struct {
	Id      int64
	RepoAt  syntax.ATURI
	LabelAt syntax.ATURI
}

func SubscribeLabel(e Execer, rl *RepoLabel) error {
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

func GetRepoLabels(e Execer, filters ...filter) ([]RepoLabel, error) {
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

	var labels []RepoLabel
	for rows.Next() {
		var label RepoLabel

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
