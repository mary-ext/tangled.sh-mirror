package db

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-git/go-git/v5/plumbing"
	spindle "tangled.sh/tangled.sh/core/spindle/models"
)

type Pipeline struct {
	Id        int
	Rkey      string
	Knot      string
	RepoOwner syntax.DID
	RepoName  string
	TriggerId int
	Sha       string
	Created   time.Time

	// populate when querying for reverse mappings
	Trigger  *Trigger
	Statuses map[string]WorkflowStatus
}

type WorkflowStatus struct {
	Data []PipelineStatus
}

func (w WorkflowStatus) Latest() PipelineStatus {
	return w.Data[len(w.Data)-1]
}

// time taken by this workflow to reach an "end state"
func (w WorkflowStatus) TimeTaken() time.Duration {
	var start, end *time.Time
	for _, s := range w.Data {
		if s.Status.IsStart() {
			start = &s.Created
		}
		if s.Status.IsFinish() {
			end = &s.Created
		}
	}

	if start != nil && end != nil && end.After(*start) {
		return end.Sub(*start)
	}

	return 0
}

func (p Pipeline) Counts() map[string]int {
	m := make(map[string]int)
	for _, w := range p.Statuses {
		m[w.Latest().Status.String()] += 1
	}
	return m
}

func (p Pipeline) TimeTaken() time.Duration {
	var s time.Duration
	for _, w := range p.Statuses {
		s += w.TimeTaken()
	}
	return s
}

func (p Pipeline) Workflows() []string {
	var ws []string
	for v := range p.Statuses {
		ws = append(ws, v)
	}
	slices.Sort(ws)
	return ws
}

// if we know that a spindle has picked up this pipeline, then it is Responding
func (p Pipeline) IsResponding() bool {
	return len(p.Statuses) != 0
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

func (t *Trigger) IsPush() bool {
	return t != nil && t.Kind == "push"
}

func (t *Trigger) IsPullRequest() bool {
	return t != nil && t.Kind == "pull_request"
}

func (t *Trigger) TargetRef() string {
	if t.IsPush() {
		return plumbing.ReferenceName(*t.PushRef).Short()
	} else if t.IsPullRequest() {
		return *t.PRTargetBranch
	}

	return ""
}

type PipelineStatus struct {
	ID           int
	Spindle      string
	Rkey         string
	PipelineKnot string
	PipelineRkey string
	Created      time.Time
	Workflow     string
	Status       spindle.StatusKind
	Error        *string
	ExitCode     int
}

func GetPipelines(e Execer, filters ...filter) ([]Pipeline, error) {
	var pipelines []Pipeline

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

	query := fmt.Sprintf(`select id, rkey, knot, repo_owner, repo_name, sha, created from pipelines %s`, whereClause)

	rows, err := e.Query(query, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pipeline Pipeline
		var createdAt string
		err = rows.Scan(
			&pipeline.Id,
			&pipeline.Rkey,
			&pipeline.Knot,
			&pipeline.RepoOwner,
			&pipeline.RepoName,
			&pipeline.Sha,
			&createdAt,
		)
		if err != nil {
			return nil, err
		}

		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			pipeline.Created = t
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
		status.Created.Format(time.RFC3339),
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
		exit_code,
		created
	) values (%s)
	`, strings.Join(placeholders, ","))

	_, err := e.Exec(query, args...)
	return err
}

// this is a mega query, but the most useful one:
// get N pipelines, for each one get the latest status of its N workflows
func GetPipelineStatuses(e Execer, filters ...filter) ([]Pipeline, error) {
	var conditions []string
	var args []any
	for _, filter := range filters {
		filter.key = "p." + filter.key // the table is aliased in the query to `p`
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(`
		select
			p.id,
			p.knot,
			p.rkey,
			p.repo_owner,
			p.repo_name,
			p.sha,
			p.created,
			t.id,
			t.kind,
			t.push_ref,
			t.push_new_sha,
			t.push_old_sha,
			t.pr_source_branch,
			t.pr_target_branch,
			t.pr_source_sha,
			t.pr_action
		from
			pipelines p
		join
			triggers t ON p.trigger_id = t.id
		%s
	`, whereClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pipelines := make(map[string]Pipeline)
	for rows.Next() {
		var p Pipeline
		var t Trigger
		var created string

		err := rows.Scan(
			&p.Id,
			&p.Knot,
			&p.Rkey,
			&p.RepoOwner,
			&p.RepoName,
			&p.Sha,
			&created,
			&p.TriggerId,
			&t.Kind,
			&t.PushRef,
			&t.PushNewSha,
			&t.PushOldSha,
			&t.PRSourceBranch,
			&t.PRTargetBranch,
			&t.PRSourceSha,
			&t.PRAction,
		)
		if err != nil {
			return nil, err
		}

		p.Created, err = time.Parse(time.RFC3339, created)
		if err != nil {
			return nil, fmt.Errorf("invalid pipeline created timestamp %q: %w", created, err)
		}

		t.Id = p.TriggerId
		p.Trigger = &t
		p.Statuses = make(map[string]WorkflowStatus)

		k := fmt.Sprintf("%s/%s", p.Knot, p.Rkey)
		pipelines[k] = p
	}

	// get all statuses
	// the where clause here is of the form:
	//
	//     where (pipeline_knot = k1 and pipeline_rkey = r1)
	//        or (pipeline_knot = k2 and pipeline_rkey = r2)
	conditions = nil
	args = nil
	for _, p := range pipelines {
		knotFilter := FilterEq("pipeline_knot", p.Knot)
		rkeyFilter := FilterEq("pipeline_rkey", p.Rkey)
		conditions = append(conditions, fmt.Sprintf("(%s and %s)", knotFilter.Condition(), rkeyFilter.Condition()))
		args = append(args, p.Knot)
		args = append(args, p.Rkey)
	}
	whereClause = ""
	if conditions != nil {
		whereClause = "where " + strings.Join(conditions, " or ")
	}
	query = fmt.Sprintf(`
		select
			id, spindle, rkey, pipeline_knot, pipeline_rkey, created, workflow, status, error, exit_code
		from
			pipeline_statuses
		%s
	`, whereClause)

	rows, err = e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ps PipelineStatus
		var created string

		err := rows.Scan(
			&ps.ID,
			&ps.Spindle,
			&ps.Rkey,
			&ps.PipelineKnot,
			&ps.PipelineRkey,
			&created,
			&ps.Workflow,
			&ps.Status,
			&ps.Error,
			&ps.ExitCode,
		)
		if err != nil {
			return nil, err
		}

		ps.Created, err = time.Parse(time.RFC3339, created)
		if err != nil {
			return nil, fmt.Errorf("invalid status created timestamp %q: %w", created, err)
		}

		key := fmt.Sprintf("%s/%s", ps.PipelineKnot, ps.PipelineRkey)

		// extract
		pipeline, ok := pipelines[key]
		if !ok {
			continue
		}
		statuses, _ := pipeline.Statuses[ps.Workflow]
		if !ok {
			pipeline.Statuses[ps.Workflow] = WorkflowStatus{}
		}

		// append
		statuses.Data = append(statuses.Data, ps)

		// reassign
		pipeline.Statuses[ps.Workflow] = statuses
		pipelines[key] = pipeline
	}

	var all []Pipeline
	for _, p := range pipelines {
		for _, s := range p.Statuses {
			slices.SortFunc(s.Data, func(a, b PipelineStatus) int {
				if a.Created.After(b.Created) {
					return 1
				}
				if a.Created.Before(b.Created) {
					return -1
				}
				if a.ID > b.ID {
					return 1
				}
				if a.ID < b.ID {
					return -1
				}
				return 0
			})
		}
		all = append(all, p)
	}

	// sort pipelines by date
	slices.SortFunc(all, func(a, b Pipeline) int {
		if a.Created.After(b.Created) {
			return -1
		}
		return 1
	})

	return all, nil
}
