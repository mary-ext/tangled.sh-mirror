package db

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Star struct {
	StarredByDid string
	RepoAt       syntax.ATURI
	Created      time.Time
	Rkey         string

	// optionally, populate this when querying for reverse mappings
	Repo *Repo
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

func GetStarStatus(e Execer, userDid string, repoAt syntax.ATURI) bool {
	if _, err := GetStar(e, userDid, repoAt); err != nil {
		return false
	} else {
		return true
	}
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
			r.created,
			r.at_uri
		from stars s
		join repos r on s.repo_at = r.at_uri
	`)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var star Star
		var repo Repo
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
			&repo.AtUri,
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
