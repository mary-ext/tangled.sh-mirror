package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tangled.org/core/appview/models"
)

func AddString(e Execer, s models.String) error {
	_, err := e.Exec(
		`insert into strings (
			did,
			rkey,
			filename,
			description,
			content,
			created,
			edited
		)
		values (?, ?, ?, ?, ?, ?, null)
		on conflict(did, rkey) do update set
			filename = excluded.filename,
			description = excluded.description,
			content = excluded.content,
			edited = case
				when
					strings.content != excluded.content
					or strings.filename != excluded.filename
					or strings.description != excluded.description then ?
				else strings.edited
			end`,
		s.Did,
		s.Rkey,
		s.Filename,
		s.Description,
		s.Contents,
		s.Created.Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)
	return err
}

func GetStrings(e Execer, limit int, filters ...filter) ([]models.String, error) {
	var all []models.String

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
		limitClause = fmt.Sprintf(" limit %d ", limit)
	}

	query := fmt.Sprintf(`select
			did,
			rkey,
			filename,
			description,
			content,
			created,
			edited
		from strings
		%s
		order by created desc
		%s`,
		whereClause,
		limitClause,
	)

	rows, err := e.Query(query, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var s models.String
		var createdAt string
		var editedAt sql.NullString

		if err := rows.Scan(
			&s.Did,
			&s.Rkey,
			&s.Filename,
			&s.Description,
			&s.Contents,
			&createdAt,
			&editedAt,
		); err != nil {
			return nil, err
		}

		s.Created, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			s.Created = time.Now()
		}

		if editedAt.Valid {
			e, err := time.Parse(time.RFC3339, editedAt.String)
			if err != nil {
				e = time.Now()
			}
			s.Edited = &e
		}

		all = append(all, s)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return all, nil
}

func CountStrings(e Execer, filters ...filter) (int64, error) {
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

	repoQuery := fmt.Sprintf(`select count(1) from strings %s`, whereClause)
	var count int64
	err := e.QueryRow(repoQuery, args...).Scan(&count)

	if !errors.Is(err, sql.ErrNoRows) && err != nil {
		return 0, err
	}

	return count, nil
}

func DeleteString(e Execer, filters ...filter) error {
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

	query := fmt.Sprintf(`delete from strings %s`, whereClause)

	_, err := e.Exec(query, args...)
	return err
}
