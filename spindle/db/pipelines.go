package db

import (
	"fmt"
)

type Pipeline struct {
	Rkey         string
	PipelineJson string
}

func (d *DB) InsertPipeline(pipeline Pipeline) error {
	_, err := d.Exec(
		`insert into pipelines (rkey, nsid, event) values (?, ?, ?)`,
		pipeline.Rkey,
		pipeline.PipelineJson,
	)

	return err
}

func (d *DB) GetPipeline(rkey, cursor string) (Pipeline, error) {
	whereClause := "where rkey = ?"
	args := []any{rkey}

	if cursor != "" {
		whereClause += " and rkey > ?"
		args = append(args, cursor)
	}

	query := fmt.Sprintf(`
		select rkey, pipeline
		from pipelines
		%s
		limit 1
	`, whereClause)

	row := d.QueryRow(query, args...)

	var p Pipeline
	err := row.Scan(&p.Rkey, &p.PipelineJson)
	if err != nil {
		return Pipeline{}, err
	}

	return p, nil
}

func (d *DB) GetPipelines(cursor string) ([]Pipeline, error) {
	whereClause := ""
	args := []any{}
	if cursor != "" {
		whereClause = "where rkey > ?"
		args = append(args, cursor)
	}

	query := fmt.Sprintf(`
		select rkey, nsid, pipeline
		from pipelines
		%s
		order by rkey asc
		limit 100
	`, whereClause)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var evts []Pipeline
	for rows.Next() {
		var ev Pipeline
		rows.Scan(&ev.Rkey, &ev.PipelineJson)
		evts = append(evts, ev)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return evts, nil
}
