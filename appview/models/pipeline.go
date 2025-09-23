package models

import (
	"slices"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-git/go-git/v5/plumbing"
	spindle "tangled.org/core/spindle/models"
	"tangled.org/core/workflow"
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
	Kind workflow.TriggerKind

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
	return t != nil && t.Kind == workflow.TriggerKindPush
}

func (t *Trigger) IsPullRequest() bool {
	return t != nil && t.Kind == workflow.TriggerKindPullRequest
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
