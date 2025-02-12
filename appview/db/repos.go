package db

import "time"

type Repo struct {
	Did     string
	Name    string
	Knot    string
	Created *time.Time
}

func (d *DB) GetAllReposByDid(did string) ([]Repo, error) {
	var repos []Repo

	rows, err := d.db.Query(`select did, name, knot, created from repos where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		var createdAt string
		if err := rows.Scan(&repo.Did, &repo.Name, &repo.Knot, &createdAt); err != nil {
			return nil, err
		}
		createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
		repo.Created = &createdAtTime
		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return repos, nil
}

func (d *DB) GetRepo(did, name string) (*Repo, error) {
	var repo Repo

	row := d.db.QueryRow(`select did, name, knot, created from repos where did = ? and name = ?`, did, name)

	var createdAt string
	if err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &createdAt); err != nil {
		return nil, err
	}
	createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
	repo.Created = &createdAtTime

	return &repo, nil
}

func (d *DB) AddRepo(repo *Repo) error {
	_, err := d.db.Exec(`insert into repos (did, name, knot) values (?, ?, ?)`, repo.Did, repo.Name, repo.Knot)
	return err
}

func (d *DB) RemoveRepo(did, name, knot string) error {
	_, err := d.db.Exec(`delete from repos where did = ? and name = ? and knot = ?`, did, name, knot)
	return err
}
