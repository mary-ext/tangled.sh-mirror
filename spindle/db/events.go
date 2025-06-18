package db

import (
	"encoding/json"
	"fmt"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/spindle/models"
	"tangled.sh/tangled.sh/core/tid"
)

type Event struct {
	Rkey      string `json:"rkey"`
	Nsid      string `json:"nsid"`
	Created   int64  `json:"created"`
	EventJson string `json:"event"`
}

func (d *DB) InsertEvent(event Event, notifier *notifier.Notifier) error {
	_, err := d.Exec(
		`insert into events (rkey, nsid, event, created) values (?, ?, ?, ?)`,
		event.Rkey,
		event.Nsid,
		event.EventJson,
		time.Now().UnixNano(),
	)

	notifier.NotifyAll()

	return err
}

func (d *DB) GetEvents(cursor int64) ([]Event, error) {
	whereClause := ""
	args := []any{}
	if cursor > 0 {
		whereClause = "where created > ?"
		args = append(args, cursor)
	}

	query := fmt.Sprintf(`
		select rkey, nsid, event, created
		from events
		%s
		order by created asc
		limit 100
	`, whereClause)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var evts []Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.Rkey, &ev.Nsid, &ev.EventJson, &ev.Created); err != nil {
			return nil, err
		}
		evts = append(evts, ev)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return evts, nil
}

func (d *DB) CreateStatusEvent(rkey string, s tangled.PipelineStatus, n *notifier.Notifier) error {
	eventJson, err := json.Marshal(s)
	if err != nil {
		return err
	}

	event := Event{
		Rkey:      rkey,
		Nsid:      tangled.PipelineStatusNSID,
		Created:   time.Now().UnixNano(),
		EventJson: string(eventJson),
	}

	return d.InsertEvent(event, n)
}

func (d *DB) createStatusEvent(
	workflowId models.WorkflowId,
	statusKind models.StatusKind,
	workflowError *string,
	exitCode *int64,
	n *notifier.Notifier,
) error {
	now := time.Now()
	pipelineAtUri := workflowId.PipelineId.AtUri()
	s := tangled.PipelineStatus{
		CreatedAt: now.Format(time.RFC3339),
		Error:     workflowError,
		ExitCode:  exitCode,
		Pipeline:  string(pipelineAtUri),
		Workflow:  workflowId.Name,
		Status:    string(statusKind),
	}

	eventJson, err := json.Marshal(s)
	if err != nil {
		return err
	}

	event := Event{
		Rkey:      tid.TID(),
		Nsid:      tangled.PipelineStatusNSID,
		Created:   now.UnixNano(),
		EventJson: string(eventJson),
	}

	return d.InsertEvent(event, n)

}

func (d *DB) StatusPending(workflowId models.WorkflowId, n *notifier.Notifier) error {
	return d.createStatusEvent(workflowId, models.StatusKindPending, nil, nil, n)
}

func (d *DB) StatusRunning(workflowId models.WorkflowId, n *notifier.Notifier) error {
	return d.createStatusEvent(workflowId, models.StatusKindRunning, nil, nil, n)
}

func (d *DB) StatusFailed(workflowId models.WorkflowId, workflowError string, exitCode int64, n *notifier.Notifier) error {
	return d.createStatusEvent(workflowId, models.StatusKindFailed, &workflowError, &exitCode, n)
}

func (d *DB) StatusSuccess(workflowId models.WorkflowId, n *notifier.Notifier) error {
	return d.createStatusEvent(workflowId, models.StatusKindSuccess, nil, nil, n)
}
