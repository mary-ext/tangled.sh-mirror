package db

import (
	"log"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Star struct {
	StarredByDid string
	RepoAt       syntax.ATURI
	Repo         *Repo
	Created      time.Time
	Rkey         string
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

func AddStar(e Execer, starredByDid string, repoAt syntax.ATURI, rkey string) error {
	query := `insert or ignore into stars (starred_by_did, repo_at, rkey) values (?, ?, ?)`
	_, err := e.Exec(query, starredByDid, repoAt, rkey)
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
