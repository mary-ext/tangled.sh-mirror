package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type Pipeline struct {
	Id        int
	Rkey      string
	Knot      string
	RepoOwner syntax.DID
	RepoName  string
	TriggerId int
	Sha       string

	// populate when querying for revers mappings
	Trigger *Trigger
}

type Trigger struct {
	Id   int
	Kind string

	// push trigger fields
	PushRef    *string
	PushNewSha *string
	PushOldSha *string

	// pull request trigger fields
	PRSourceBranch *string
	PRTargetBranch *string
	PRSourceSha    *string
	PRAction       *string
}

type PipelineStatus struct {
	ID           int
	Spindle      string
	Rkey         string
	PipelineKnot string
	PipelineRkey string
	Created      time.Time
	Workflow     string
	Status       string
	Error        *string
	ExitCode     int
}

func GetPipelines(e Execer, filters ...filter) ([]Pipeline, error) {
	var pipelines []Pipeline

	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.arg)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(`select id, rkey, knot, repo_owner, repo_name, sha from pipelines %s`, whereClause)

	rows, err := e.Query(query, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pipeline Pipeline
		err = rows.Scan(
			&pipeline.Id,
			&pipeline.Rkey,
			&pipeline.Knot,
			&pipeline.RepoOwner,
			&pipeline.RepoName,
			&pipeline.Sha,
		)
		if err != nil {
			return nil, err
		}

		pipelines = append(pipelines, pipeline)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return pipelines, nil
}

func AddPipeline(e Execer, pipeline Pipeline) error {
	args := []any{
		pipeline.Rkey,
		pipeline.Knot,
		pipeline.RepoOwner,
		pipeline.RepoName,
		pipeline.TriggerId,
		pipeline.Sha,
	}

	placeholders := make([]string, len(args))
	for i := range placeholders {
		placeholders[i] = "?"
	}

	query := fmt.Sprintf(`
	insert or ignore into pipelines (
		rkey,
		knot,
		repo_owner,
		repo_name,
		trigger_id,
		sha
	) values (%s)
	`, strings.Join(placeholders, ","))

	_, err := e.Exec(query, args...)

	return err
}

func AddTrigger(e Execer, trigger Trigger) (int64, error) {
	args := []any{
		trigger.Kind,
		trigger.PushRef,
		trigger.PushNewSha,
		trigger.PushOldSha,
		trigger.PRSourceBranch,
		trigger.PRTargetBranch,
		trigger.PRSourceSha,
		trigger.PRAction,
	}

	placeholders := make([]string, len(args))
	for i := range placeholders {
		placeholders[i] = "?"
	}

	query := fmt.Sprintf(`insert or ignore into triggers (
		kind,
		push_ref,
		push_new_sha,
		push_old_sha,
		pr_source_branch,
		pr_target_branch,
		pr_source_sha,
		pr_action
	) values (%s)`, strings.Join(placeholders, ","))

	res, err := e.Exec(query, args...)
	if err != nil {
		return 0, err
	}

	return res.LastInsertId()
}

func AddPipelineStatus(e Execer, status PipelineStatus) error {
	args := []any{
		status.Spindle,
		status.Rkey,
		status.PipelineKnot,
		status.PipelineRkey,
		status.Workflow,
		status.Status,
		status.Error,
		status.ExitCode,
	}

	placeholders := make([]string, len(args))
	for i := range placeholders {
		placeholders[i] = "?"
	}

	query := fmt.Sprintf(`
	insert or ignore into pipeline_statuses (
		spindle,
		rkey,
		pipeline_knot,
		pipeline_rkey,
		workflow,
		status,
		error,
		exit_code
	) values (%s)
	`, strings.Join(placeholders, ","))

	_, err := e.Exec(query, args...)
	return err
}
