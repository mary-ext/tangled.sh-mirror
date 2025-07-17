package db

import (
	"fmt"
	"strings"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type RepoLanguage struct {
	Id       int64
	RepoAt   syntax.ATURI
	Ref      string
	Language string
	Bytes    int64
}

func GetRepoLanguages(e Execer, filters ...filter) ([]RepoLanguage, error) {
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

	query := fmt.Sprintf(
		`select id, repo_at, ref, language, bytes from repo_languages %s`,
		whereClause,
	)
	rows, err := e.Query(query, args...)

	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w ", err)
	}

	var langs []RepoLanguage
	for rows.Next() {
		var rl RepoLanguage

		err := rows.Scan(
			&rl.Id,
			&rl.RepoAt,
			&rl.Ref,
			&rl.Language,
			&rl.Bytes,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan: %w ", err)
		}

		langs = append(langs, rl)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan rows: %w ", err)
	}

	return langs, nil
}

func InsertRepoLanguages(e Execer, langs []RepoLanguage) error {
	stmt, err := e.Prepare(
		"insert or replace into repo_languages (repo_at, ref, language, bytes) values (?, ?, ?, ?)",
	)
	if err != nil {
		return err
	}

	for _, l := range langs {
		_, err := stmt.Exec(l.RepoAt, l.Ref, l.Language, l.Bytes)
		if err != nil {
			return err
		}
	}

	return nil
}
