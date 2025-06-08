package db

import (
	"fmt"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/notifier"
)

type PipelineStatus string

var (
	PipelinePending   PipelineStatus = "pending"
	PipelineRunning   PipelineStatus = "running"
	PipelineFailed    PipelineStatus = "failed"
	PipelineTimeout   PipelineStatus = "timeout"
	PipelineCancelled PipelineStatus = "cancelled"
	PipelineSuccess   PipelineStatus = "success"
)

type Pipeline struct {
	Rkey   string         `json:"rkey"`
	Knot   string         `json:"knot"`
	Status PipelineStatus `json:"status"`

	// only if Failed
	Error    string `json:"error"`
	ExitCode int    `json:"exit_code"`

	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	FinishedAt time.Time `json:"finished_at"`
}

func (p Pipeline) AsRecord() *tangled.PipelineStatus {
	exitCode64 := int64(p.ExitCode)
	finishedAt := p.FinishedAt.String()

	return &tangled.PipelineStatus{
		Pipeline: fmt.Sprintf("at://%s/%s", p.Knot, p.Rkey),
		Status:   string(p.Status),

		ExitCode: &exitCode64,
		Error:    &p.Error,

		StartedAt:  p.StartedAt.String(),
		UpdatedAt:  p.UpdatedAt.String(),
		FinishedAt: &finishedAt,
	}
}

func pipelineAtUri(rkey, knot string) string {
	return fmt.Sprintf("at://%s/did:web:%s/%s", tangled.PipelineStatusNSID, knot, rkey)
}

func (db *DB) CreatePipeline(rkey, knot string, n *notifier.Notifier) error {
	_, err := db.Exec(`
		insert into pipelines (at_uri, status)
		values (?, ?)
	`, pipelineAtUri(rkey, knot), PipelinePending)

	if err != nil {
		return err
	}
	n.NotifyAll()
	return nil
}

func (db *DB) MarkPipelineRunning(rkey, knot string, n *notifier.Notifier) error {
	_, err := db.Exec(`
			update pipelines
			set status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
			where at_uri = ?
		`, PipelineRunning, pipelineAtUri(rkey, knot))

	if err != nil {
		return err
	}
	n.NotifyAll()
	return nil
}

func (db *DB) MarkPipelineFailed(rkey, knot string, exitCode int, errorMsg string, n *notifier.Notifier) error {
	_, err := db.Exec(`
		update pipelines
		set status = ?,
		    exit_code = ?,
		    error = ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
		    finished_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		where at_uri = ?
	`, PipelineFailed, exitCode, errorMsg, pipelineAtUri(rkey, knot))
	if err != nil {
		return err
	}
	n.NotifyAll()
	return nil
}

func (db *DB) MarkPipelineTimeout(rkey, knot string, n *notifier.Notifier) error {
	_, err := db.Exec(`
			update pipelines
			set status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
			where at_uri = ?
		`, PipelineTimeout, pipelineAtUri(rkey, knot))
	if err != nil {
		return err
	}
	n.NotifyAll()
	return nil
}

func (db *DB) MarkPipelineSuccess(rkey, knot string, n *notifier.Notifier) error {
	_, err := db.Exec(`
			update pipelines
			set status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
			finished_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
			where at_uri = ?
		`, PipelineSuccess, pipelineAtUri(rkey, knot))

	if err != nil {
		return err
	}
	n.NotifyAll()
	return nil
}

func (db *DB) GetPipeline(rkey, knot string) (Pipeline, error) {
	var p Pipeline
	err := db.QueryRow(`
		select rkey, status, error, exit_code, started_at, updated_at, finished_at
		from pipelines
		where at_uri = ?
	`, pipelineAtUri(rkey, knot)).Scan(&p.Rkey, &p.Status, &p.Error, &p.ExitCode, &p.StartedAt, &p.UpdatedAt, &p.FinishedAt)
	return p, err
}

func (db *DB) GetPipelines(cursor string) ([]Pipeline, error) {
	whereClause := ""
	args := []any{}
	if cursor != "" {
		whereClause = "where rkey > ?"
		args = append(args, cursor)
	}

	query := fmt.Sprintf(`
		select rkey, status, error, exit_code, started_at, updated_at, finished_at
		from pipelines
		%s
		order by rkey asc
		limit 100
	`, whereClause)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pipelines []Pipeline
	for rows.Next() {
		var p Pipeline
		rows.Scan(&p.Rkey, &p.Status, &p.Error, &p.ExitCode, &p.StartedAt, &p.UpdatedAt, &p.FinishedAt)
		pipelines = append(pipelines, p)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pipelines, nil
}
