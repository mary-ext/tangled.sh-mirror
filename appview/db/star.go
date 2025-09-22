package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/models"
)

type Star struct {
	StarredByDid string
	RepoAt       syntax.ATURI
	Created      time.Time
	Rkey         string

	// optionally, populate this when querying for reverse mappings
	Repo *models.Repo
}

func (star *Star) ResolveRepo(e Execer) error {
	if star.Repo != nil {
		return nil
	}

	repo, err := GetRepoByAtUri(e, star.RepoAt.String())
	if err != nil {
		return err
	}

	star.Repo = repo
	return nil
}

func AddStar(e Execer, star *Star) error {
	query := `insert or ignore into stars (starred_by_did, repo_at, rkey) values (?, ?, ?)`
	_, err := e.Exec(
		query,
		star.StarredByDid,
		star.RepoAt.String(),
		star.Rkey,
	)
	return err
}

// Get a star record
func GetStar(e Execer, starredByDid string, repoAt syntax.ATURI) (*Star, error) {
	query := `
	select starred_by_did, repo_at, created, rkey
	from stars
	where starred_by_did = ? and repo_at = ?`
	row := e.QueryRow(query, starredByDid, repoAt)

	var star Star
	var created string
	err := row.Scan(&star.StarredByDid, &star.RepoAt, &created, &star.Rkey)
	if err != nil {
		return nil, err
	}

	createdAtTime, err := time.Parse(time.RFC3339, created)
	if err != nil {
		log.Println("unable to determine followed at time")
		star.Created = time.Now()
	} else {
		star.Created = createdAtTime
	}

	return &star, nil
}

// Remove a star
func DeleteStar(e Execer, starredByDid string, repoAt syntax.ATURI) error {
	_, err := e.Exec(`delete from stars where starred_by_did = ? and repo_at = ?`, starredByDid, repoAt)
	return err
}

// Remove a star
func DeleteStarByRkey(e Execer, starredByDid string, rkey string) error {
	_, err := e.Exec(`delete from stars where starred_by_did = ? and rkey = ?`, starredByDid, rkey)
	return err
}

func GetStarCount(e Execer, repoAt syntax.ATURI) (int, error) {
	stars := 0
	err := e.QueryRow(
		`select count(starred_by_did) from stars where repo_at = ?`, repoAt).Scan(&stars)
	if err != nil {
		return 0, err
	}
	return stars, nil
}

// getStarStatuses returns a map of repo URIs to star status for a given user
// This is an internal helper function to avoid N+1 queries
func getStarStatuses(e Execer, userDid string, repoAts []syntax.ATURI) (map[string]bool, error) {
	if len(repoAts) == 0 || userDid == "" {
		return make(map[string]bool), nil
	}

	placeholders := make([]string, len(repoAts))
	args := make([]any, len(repoAts)+1)
	args[0] = userDid

	for i, repoAt := range repoAts {
		placeholders[i] = "?"
		args[i+1] = repoAt.String()
	}

	query := fmt.Sprintf(`
		SELECT repo_at
		FROM stars
		WHERE starred_by_did = ? AND repo_at IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool)
	// Initialize all repos as not starred
	for _, repoAt := range repoAts {
		result[repoAt.String()] = false
	}

	// Mark starred repos as true
	for rows.Next() {
		var repoAt string
		if err := rows.Scan(&repoAt); err != nil {
			return nil, err
		}
		result[repoAt] = true
	}

	return result, nil
}

func GetStarStatus(e Execer, userDid string, repoAt syntax.ATURI) bool {
	statuses, err := getStarStatuses(e, userDid, []syntax.ATURI{repoAt})
	if err != nil {
		return false
	}
	return statuses[repoAt.String()]
}

// GetStarStatuses returns a map of repo URIs to star status for a given user
func GetStarStatuses(e Execer, userDid string, repoAts []syntax.ATURI) (map[string]bool, error) {
	return getStarStatuses(e, userDid, repoAts)
}
func GetStars(e Execer, limit int, filters ...filter) ([]Star, error) {
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
		`select starred_by_did, repo_at, created, rkey
		from stars
		%s
		order by created desc
		%s`,
		whereClause,
		limitClause,
	)
	rows, err := e.Query(repoQuery, args...)
	if err != nil {
		return nil, err
	}

	starMap := make(map[string][]Star)
	for rows.Next() {
		var star Star
		var created string
		err := rows.Scan(&star.StarredByDid, &star.RepoAt, &created, &star.Rkey)
		if err != nil {
			return nil, err
		}

		star.Created = time.Now()
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			star.Created = t
		}

		repoAt := string(star.RepoAt)
		starMap[repoAt] = append(starMap[repoAt], star)
	}

	// populate *Repo in each star
	args = make([]any, len(starMap))
	i := 0
	for r := range starMap {
		args[i] = r
		i++
	}

	if len(args) == 0 {
		return nil, nil
	}

	repos, err := GetRepos(e, 0, FilterIn("at_uri", args))
	if err != nil {
		return nil, err
	}

	for _, r := range repos {
		if stars, ok := starMap[string(r.RepoAt())]; ok {
			for i := range stars {
				stars[i].Repo = &r
			}
		}
	}

	var stars []Star
	for _, s := range starMap {
		stars = append(stars, s...)
	}

	return stars, nil
}

func CountStars(e Execer, filters ...filter) (int64, error) {
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

	repoQuery := fmt.Sprintf(`select count(1) from stars %s`, whereClause)
	var count int64
	err := e.QueryRow(repoQuery, args...).Scan(&count)

	if !errors.Is(err, sql.ErrNoRows) && err != nil {
		return 0, err
	}

	return count, nil
}

func GetAllStars(e Execer, limit int) ([]Star, error) {
	var stars []Star

	rows, err := e.Query(`
		select
			s.starred_by_did,
			s.repo_at,
			s.rkey,
			s.created,
			r.did,
			r.name,
			r.knot,
			r.rkey,
			r.created
		from stars s
		join repos r on s.repo_at = r.at_uri
	`)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var star Star
		var repo models.Repo
		var starCreatedAt, repoCreatedAt string

		if err := rows.Scan(
			&star.StarredByDid,
			&star.RepoAt,
			&star.Rkey,
			&starCreatedAt,
			&repo.Did,
			&repo.Name,
			&repo.Knot,
			&repo.Rkey,
			&repoCreatedAt,
		); err != nil {
			return nil, err
		}

		star.Created, err = time.Parse(time.RFC3339, starCreatedAt)
		if err != nil {
			star.Created = time.Now()
		}
		repo.Created, err = time.Parse(time.RFC3339, repoCreatedAt)
		if err != nil {
			repo.Created = time.Now()
		}
		star.Repo = &repo

		stars = append(stars, star)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return stars, nil
}

// GetTopStarredReposLastWeek returns the top 8 most starred repositories from the last week
func GetTopStarredReposLastWeek(e Execer) ([]models.Repo, error) {
	// first, get the top repo URIs by star count from the last week
	query := `
		with recent_starred_repos as (
			select distinct repo_at
			from stars
			where created >= datetime('now', '-7 days')
		),
		repo_star_counts as (
			select
				s.repo_at,
				count(*) as stars_gained_last_week
			from stars s
			join recent_starred_repos rsr on s.repo_at = rsr.repo_at
			where s.created >= datetime('now', '-7 days')
			group by s.repo_at
		)
		select rsc.repo_at
		from repo_star_counts rsc
		order by rsc.stars_gained_last_week desc
		limit 8
	`

	rows, err := e.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repoUris []string
	for rows.Next() {
		var repoUri string
		err := rows.Scan(&repoUri)
		if err != nil {
			return nil, err
		}
		repoUris = append(repoUris, repoUri)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(repoUris) == 0 {
		return []models.Repo{}, nil
	}

	// get full repo data
	repos, err := GetRepos(e, 0, FilterIn("at_uri", repoUris))
	if err != nil {
		return nil, err
	}

	// sort repos by the original trending order
	repoMap := make(map[string]models.Repo)
	for _, repo := range repos {
		repoMap[repo.RepoAt().String()] = repo
	}

	orderedRepos := make([]models.Repo, 0, len(repoUris))
	for _, uri := range repoUris {
		if repo, exists := repoMap[uri]; exists {
			orderedRepos = append(orderedRepos, repo)
		}
	}

	return orderedRepos, nil
}
