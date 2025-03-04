package db

import (
	"database/sql"
	"time"
)

type Repo struct {
	Did     string
	Name    string
	Knot    string
	Rkey    string
	Created time.Time
	AtUri   string
}

func GetAllRepos(e Execer) ([]Repo, error) {
	var repos []Repo

	rows, err := e.Query(`select did, name, knot, rkey, created from repos`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		err := scanRepo(rows, &repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &repo.Created)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return repos, nil
}

func GetAllReposByDid(e Execer, did string) ([]Repo, error) {
	var repos []Repo

	rows, err := e.Query(`select did, name, knot, rkey, created from repos where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		err := scanRepo(rows, &repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &repo.Created)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return repos, nil
}

func GetRepo(e Execer, did, name string) (*Repo, error) {
	var repo Repo

	row := e.QueryRow(`select did, name, knot, created, at_uri from repos where did = ? and name = ?`, did, name)

	var createdAt string
	if err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &createdAt, &repo.AtUri); err != nil {
		return nil, err
	}
	createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
	repo.Created = createdAtTime

	return &repo, nil
}

func AddRepo(e Execer, repo *Repo) error {
	_, err := e.Exec(`insert into repos (did, name, knot, rkey, at_uri) values (?, ?, ?, ?, ?)`, repo.Did, repo.Name, repo.Knot, repo.Rkey, repo.AtUri)
	return err
}

func RemoveRepo(e Execer, did, name, rkey string) error {
	_, err := e.Exec(`delete from repos where did = ? and name = ? and rkey = ?`, did, name, rkey)
	return err
}

func AddCollaborator(e Execer, collaborator, repoOwnerDid, repoName, repoKnot string) error {
	_, err := e.Exec(
		`insert into collaborators (did, repo)
		values (?, (select id from repos where did = ? and name = ? and knot = ?));`,
		collaborator, repoOwnerDid, repoName, repoKnot)
	return err
}

func CollaboratingIn(e Execer, collaborator string) ([]Repo, error) {
	var repos []Repo

	rows, err := e.Query(`select r.did, r.name, r.knot, r.rkey, r.created from repos r join collaborators c on r.id = c.repo where c.did = ?;`, collaborator)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		err := scanRepo(rows, &repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &repo.Created)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return repos, nil
}

func scanRepo(rows *sql.Rows, did, name, knot, rkey *string, created *time.Time) error {
	var createdAt string
	if err := rows.Scan(did, name, knot, rkey, &createdAt); err != nil {
		return err
	}

	createdAtTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		*created = time.Now()
	} else {
		*created = createdAtTime
	}

	return nil
}
