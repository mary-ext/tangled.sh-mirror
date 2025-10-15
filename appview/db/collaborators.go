package db

import (
	"fmt"
	"strings"
	"time"

	"tangled.org/core/appview/models"
)

func AddCollaborator(e Execer, c models.Collaborator) error {
	_, err := e.Exec(
		`insert into collaborators (did, rkey, subject_did, repo_at) values (?, ?, ?, ?);`,
		c.Did, c.Rkey, c.SubjectDid, c.RepoAt,
	)
	return err
}

func DeleteCollaborator(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`delete from collaborators %s`, whereClause)

	_, err := e.Exec(query, args...)
	return err
}

func CollaboratingIn(e Execer, collaborator string) ([]models.Repo, error) {
	rows, err := e.Query(`select repo_at from collaborators where subject_did = ?`, collaborator)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repoAts []string
	for rows.Next() {
		var aturi string
		err := rows.Scan(&aturi)
		if err != nil {
			return nil, err
		}
		repoAts = append(repoAts, aturi)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if repoAts == nil {
		return nil, nil
	}

	return GetRepos(e, 0, FilterIn("at_uri", repoAts))
}

func GetCollaborators(e Execer, filters ...filter) ([]models.Collaborator, error) {
	var collaborators []models.Collaborator
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
	query := fmt.Sprintf(`select
			id,
			did,
			rkey,
			subject_did,
			repo_at,
			created
		from collaborators %s`,
		whereClause,
	)
	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var collaborator models.Collaborator
		var createdAt string
		if err := rows.Scan(
			&collaborator.Id,
			&collaborator.Did,
			&collaborator.Rkey,
			&collaborator.SubjectDid,
			&collaborator.RepoAt,
			&createdAt,
		); err != nil {
			return nil, err
		}
		collaborator.Created, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			collaborator.Created = time.Now()
		}
		collaborators = append(collaborators, collaborator)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return collaborators, nil
}
