package db

//
// import (
// 	"database/sql"
// 	"fmt"
// 	"time"
//
// 	"tangled.sh/tangled.sh/core/api/tangled"
// 	"tangled.sh/tangled.sh/core/notifier"
// )
//
// type PipelineRunStatus string
//
// var (
// 	PipelinePending   PipelineRunStatus = "pending"
// 	PipelineRunning   PipelineRunStatus = "running"
// 	PipelineFailed    PipelineRunStatus = "failed"
// 	PipelineTimeout   PipelineRunStatus = "timeout"
// 	PipelineCancelled PipelineRunStatus = "cancelled"
// 	PipelineSuccess   PipelineRunStatus = "success"
// )
//
// type PipelineStatus struct {
// 	Rkey     string            `json:"rkey"`
// 	Pipeline string            `json:"pipeline"`
// 	Status   PipelineRunStatus `json:"status"`
//
// 	// only if Failed
// 	Error    string `json:"error"`
// 	ExitCode int    `json:"exit_code"`
//
// 	LastUpdate int64     `json:"last_update"`
// 	StartedAt  time.Time `json:"started_at"`
// 	UpdatedAt  time.Time `json:"updated_at"`
// 	FinishedAt time.Time `json:"finished_at"`
// }
//
// func (p PipelineStatus) AsRecord() *tangled.PipelineStatus {
// 	exitCode64 := int64(p.ExitCode)
// 	finishedAt := p.FinishedAt.String()
//
// 	return &tangled.PipelineStatus{
// 		LexiconTypeID: tangled.PipelineStatusNSID,
// 		Pipeline:      p.Pipeline,
// 		Status:        string(p.Status),
//
// 		ExitCode: &exitCode64,
// 		Error:    &p.Error,
//
// 		StartedAt:  p.StartedAt.String(),
// 		UpdatedAt:  p.UpdatedAt.String(),
// 		FinishedAt: &finishedAt,
// 	}
// }
//
// func pipelineAtUri(rkey, knot string) string {
// 	return fmt.Sprintf("at://%s/did:web:%s/%s", tangled.PipelineStatusNSID, knot, rkey)
// }
//
// func (db *DB) CreatePipeline(rkey, pipeline string, n *notifier.Notifier) error {
// 	_, err := db.Exec(`
// 		insert into pipeline_status (rkey, status, pipeline, last_update)
// 		values (?, ?, ?, ?)
// 	`, rkey, PipelinePending, pipeline, time.Now().UnixNano())
//
// 	if err != nil {
// 		return err
// 	}
// 	n.NotifyAll()
// 	return nil
// }
//
// func (db *DB) MarkPipelineRunning(rkey string, n *notifier.Notifier) error {
// 	_, err := db.Exec(`
// 			update pipeline_status
// 			set status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), last_update = ?
// 			where rkey = ?
// 		`, PipelineRunning, rkey, time.Now().UnixNano())
//
// 	if err != nil {
// 		return err
// 	}
// 	n.NotifyAll()
// 	return nil
// }
//
// func (db *DB) MarkPipelineFailed(rkey string, exitCode int, errorMsg string, n *notifier.Notifier) error {
// 	_, err := db.Exec(`
// 		update pipeline_status
// 		set status = ?,
// 		    exit_code = ?,
// 		    error = ?,
// 		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
// 		    finished_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
// 			last_update = ?
// 		where rkey = ?
// 	`, PipelineFailed, exitCode, errorMsg, rkey, time.Now().UnixNano())
// 	if err != nil {
// 		return err
// 	}
// 	n.NotifyAll()
// 	return nil
// }
//
// func (db *DB) MarkPipelineTimeout(rkey string, n *notifier.Notifier) error {
// 	_, err := db.Exec(`
// 			update pipeline_status
// 			set status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
// 			where rkey = ?
// 		`, PipelineTimeout, rkey)
// 	if err != nil {
// 		return err
// 	}
// 	n.NotifyAll()
// 	return nil
// }
//
// func (db *DB) MarkPipelineSuccess(rkey string, n *notifier.Notifier) error {
// 	_, err := db.Exec(`
// 			update pipeline_status
// 			set status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
// 			finished_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
// 			where rkey = ?
// 		`, PipelineSuccess, rkey)
//
// 	if err != nil {
// 		return err
// 	}
// 	n.NotifyAll()
// 	return nil
// }
//
// func (db *DB) GetPipelineStatus(rkey string) (PipelineStatus, error) {
// 	var p PipelineStatus
// 	err := db.QueryRow(`
// 		select rkey, status, error, exit_code, started_at, updated_at, finished_at
// 		from pipelines
// 		where rkey = ?
// 	`, rkey).Scan(&p.Rkey, &p.Status, &p.Error, &p.ExitCode, &p.StartedAt, &p.UpdatedAt, &p.FinishedAt)
// 	return p, err
// }
//
// func (db *DB) GetPipelineStatusAsRecords(cursor int64) ([]PipelineStatus, error) {
// 	whereClause := ""
// 	args := []any{}
// 	if cursor != 0 {
// 		whereClause = "where created_at > ?"
// 		args = append(args, cursor)
// 	}
//
// 	query := fmt.Sprintf(`
// 		select rkey, status, error, exit_code, created_at, started_at, updated_at, finished_at
// 		from pipeline_status
// 		%s
// 		order by created_at asc
// 		limit 100
// 	`, whereClause)
//
// 	rows, err := db.Query(query, args...)
// 	if err != nil {
// 		return nil, err
// 	}
// 	defer rows.Close()
//
// 	var pipelines []PipelineStatus
// 	for rows.Next() {
// 		var p PipelineStatus
// 		var pipelineError sql.NullString
// 		var exitCode sql.NullInt64
// 		var startedAt, updatedAt string
// 		var finishedAt sql.NullTime
//
// 		err := rows.Scan(&p.Rkey, &p.Status, &pipelineError, &exitCode, &p.LastUpdate, &startedAt, &updatedAt, &finishedAt)
// 		if err != nil {
// 			return nil, err
// 		}
//
// 		if pipelineError.Valid {
// 			p.Error = pipelineError.String
// 		}
//
// 		if exitCode.Valid {
// 			p.ExitCode = int(exitCode.Int64)
// 		}
//
// 		if v, err := time.Parse(time.RFC3339, startedAt); err == nil {
// 			p.StartedAt = v
// 		}
//
// 		if v, err := time.Parse(time.RFC3339, updatedAt); err == nil {
// 			p.UpdatedAt = v
// 		}
//
// 		if finishedAt.Valid {
// 			p.FinishedAt = finishedAt.Time
// 		}
//
// 		pipelines = append(pipelines, p)
// 	}
//
// 	if err := rows.Err(); err != nil {
// 		return nil, err
// 	}
//
// 	return pipelines, nil
// }
