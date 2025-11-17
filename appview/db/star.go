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

func AddStar(e Execer, star *models.Star) error {
	query := `insert or ignore into stars (did, subject_at, rkey) values (?, ?, ?)`
	_, err := e.Exec(
		query,
		star.Did,
		star.RepoAt.String(),
		star.Rkey,
	)
	return err
}

// Get a star record
func GetStar(e Execer, did string, subjectAt syntax.ATURI) (*models.Star, error) {
	query := `
	select did, subject_at, created, rkey
	from stars
	where did = ? and subject_at = ?`
	row := e.QueryRow(query, did, subjectAt)

	var star models.Star
	var created string
	err := row.Scan(&star.Did, &star.RepoAt, &created, &star.Rkey)
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
func DeleteStar(e Execer, did string, subjectAt syntax.ATURI) error {
	_, err := e.Exec(`delete from stars where did = ? and subject_at = ?`, did, subjectAt)
	return err
}

// Remove a star
func DeleteStarByRkey(e Execer, did string, rkey string) error {
	_, err := e.Exec(`delete from stars where did = ? and rkey = ?`, did, rkey)
	return err
}

func GetStarCount(e Execer, subjectAt syntax.ATURI) (int, error) {
	stars := 0
	err := e.QueryRow(
		`select count(did) from stars where subject_at = ?`, subjectAt).Scan(&stars)
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
		SELECT subject_at
		FROM stars
		WHERE did = ? AND subject_at IN (%s)
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

func GetStarStatus(e Execer, userDid string, subjectAt syntax.ATURI) bool {
	statuses, err := getStarStatuses(e, userDid, []syntax.ATURI{subjectAt})
	if err != nil {
		return false
	}
	return statuses[subjectAt.String()]
}

// GetStarStatuses returns a map of repo URIs to star status for a given user
func GetStarStatuses(e Execer, userDid string, subjectAts []syntax.ATURI) (map[string]bool, error) {
	return getStarStatuses(e, userDid, subjectAts)
}

// GetRepoStars return a list of stars each holding target repository.
// If there isn't known repo with starred at-uri, those stars will be ignored.
func GetRepoStars(e Execer, limit int, filters ...filter) ([]models.RepoStar, error) {
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
		`select did, subject_at, created, rkey
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

	starMap := make(map[string][]models.Star)
	for rows.Next() {
		var star models.Star
		var created string
		err := rows.Scan(&star.Did, &star.RepoAt, &created, &star.Rkey)
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

	var repoStars []models.RepoStar
	for _, r := range repos {
		if stars, ok := starMap[string(r.RepoAt())]; ok {
			for _, star := range stars {
				repoStars = append(repoStars, models.RepoStar{
					Star: star,
					Repo: &r,
				})
			}
		}
	}

	slices.SortFunc(repoStars, func(a, b models.RepoStar) int {
		if a.Created.After(b.Created) {
			return -1
		}
		if b.Created.After(a.Created) {
			return 1
		}
		return 0
	})

	return repoStars, nil
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

// GetTopStarredReposLastWeek returns the top 8 most starred repositories from the last week
func GetTopStarredReposLastWeek(e Execer) ([]models.Repo, error) {
	// first, get the top repo URIs by star count from the last week
	query := `
		with recent_starred_repos as (
			select distinct subject_at
			from stars
			where created >= datetime('now', '-7 days')
		),
		repo_star_counts as (
			select
				s.subject_at,
				count(*) as stars_gained_last_week
			from stars s
			join recent_starred_repos rsr on s.subject_at = rsr.subject_at
			where s.created >= datetime('now', '-7 days')
			group by s.subject_at
		)
		select rsc.subject_at
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
