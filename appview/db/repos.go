package db

import (
	"database/sql"
	"time"
)

type Repo struct {
	Did         string
	Name        string
	Knot        string
	Rkey        string
	Created     time.Time
	AtUri       string
	Description string

	// optionally, populate this when querying for reverse mappings
	RepoStats *RepoStats
}

func GetAllRepos(e Execer, limit int) ([]Repo, error) {
	var repos []Repo

	rows, err := e.Query(
		`select did, name, knot, rkey, description, created 
		from repos
		order by created desc
		limit ?
		`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		err := scanRepo(
			rows, &repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &repo.Description, &repo.Created,
		)
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

	rows, err := e.Query(
		`select
			r.did,
			r.name,
			r.knot,
			r.rkey,
			r.description,
			r.created,
			count(s.id) as star_count
		from
			repos r
		left join
			stars s on r.at_uri = s.repo_at
		where
			r.did = ?
		group by
			r.at_uri`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		var repoStats RepoStats
		var createdAt string
		var nullableDescription sql.NullString

		err := rows.Scan(&repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &createdAt, &repoStats.StarCount)
		if err != nil {
			return nil, err
		}

		if nullableDescription.Valid {
			repo.Description = nullableDescription.String
		} else {
			repo.Description = ""
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			repo.Created = time.Now()
		} else {
			repo.Created = createdAtTime
		}

		repo.RepoStats = &repoStats

		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return repos, nil
}

func GetRepo(e Execer, did, name string) (*Repo, error) {
	var repo Repo
	var nullableDescription sql.NullString

	row := e.QueryRow(`select did, name, knot, created, at_uri, description from repos where did = ? and name = ?`, did, name)

	var createdAt string
	if err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &createdAt, &repo.AtUri, &nullableDescription); err != nil {
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

func GetRepoByAtUri(e Execer, atUri string) (*Repo, error) {
	var repo Repo
	var nullableDescription sql.NullString

	row := e.QueryRow(`select did, name, knot, created, at_uri, description from repos where at_uri = ?`, atUri)

	var createdAt string
	if err := row.Scan(&repo.Did, &repo.Name, &repo.Knot, &createdAt, &repo.AtUri, &nullableDescription); err != nil {
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
		(did, name, knot, rkey, at_uri, description)
		values (?, ?, ?, ?, ?, ?)`,
		repo.Did, repo.Name, repo.Knot, repo.Rkey, repo.AtUri, repo.Description,
	)
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

func UpdateDescription(e Execer, repoAt, newDescription string) error {
	_, err := e.Exec(
		`update repos set description = ? where at_uri = ?`, newDescription, repoAt)
	return err
}

func CollaboratingIn(e Execer, collaborator string) ([]Repo, error) {
	var repos []Repo

	rows, err := e.Query(
		`select
			r.did, r.name, r.knot, r.rkey, r.description, r.created, count(s.id) as star_count
		from
			repos r
		join
			collaborators c on r.id = c.repo
		left join
			stars s on r.at_uri = s.repo_at
		where
			c.did = ?
		group by
			r.id;`, collaborator)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var repo Repo
		var repoStats RepoStats
		var createdAt string
		var nullableDescription sql.NullString

		err := rows.Scan(&repo.Did, &repo.Name, &repo.Knot, &repo.Rkey, &nullableDescription, &createdAt, &repoStats.StarCount)
		if err != nil {
			return nil, err
		}

		if nullableDescription.Valid {
			repo.Description = nullableDescription.String
		} else {
			repo.Description = ""
		}

		createdAtTime, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			repo.Created = time.Now()
		} else {
			repo.Created = createdAtTime
		}

		repo.RepoStats = &repoStats

		repos = append(repos, repo)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return repos, nil
}

type RepoStats struct {
	StarCount  int
	IssueCount IssueCount
}

func scanRepo(rows *sql.Rows, did, name, knot, rkey, description *string, created *time.Time) error {
	var createdAt string
	var nullableDescription sql.NullString
	if err := rows.Scan(did, name, knot, rkey, &nullableDescription, &createdAt); err != nil {
		return err
	}

	if nullableDescription.Valid {
		*description = nullableDescription.String
	} else {
		*description = ""
	}

	createdAtTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		*created = time.Now()
	} else {
		*created = createdAtTime
	}

	return nil
}
