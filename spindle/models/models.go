package models

import (
	"fmt"
	"regexp"
	"slices"

	"tangled.sh/tangled.sh/core/api/tangled"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

var (
	re = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
)

type PipelineId struct {
	Knot string
	Rkey string
}

func (p *PipelineId) AtUri() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://did:web:%s/%s/%s", p.Knot, tangled.PipelineNSID, p.Rkey))
}

type WorkflowId struct {
	PipelineId
	Name string
}

func (wid WorkflowId) String() string {
	return fmt.Sprintf("%s-%s-%s", normalize(wid.Knot), wid.Rkey, normalize(wid.Name))
}

func normalize(name string) string {
	normalized := re.ReplaceAllString(name, "-")
	return normalized
}

type StatusKind string

var (
	StatusKindPending   StatusKind = "pending"
	StatusKindRunning   StatusKind = "running"
	StatusKindFailed    StatusKind = "failed"
	StatusKindTimeout   StatusKind = "timeout"
	StatusKindCancelled StatusKind = "cancelled"
	StatusKindSuccess   StatusKind = "success"

	StartStates [2]StatusKind = [2]StatusKind{
		StatusKindPending,
		StatusKindRunning,
	}
	FinishStates [4]StatusKind = [4]StatusKind{
		StatusKindCancelled,
		StatusKindFailed,
		StatusKindSuccess,
		StatusKindTimeout,
	}
)

func (s StatusKind) String() string {
	return string(s)
}

func (s StatusKind) IsStart() bool {
	return slices.Contains(StartStates[:], s)
}

func (s StatusKind) IsFinish() bool {
	return slices.Contains(FinishStates[:], s)
}

type LogKind string

var (
	// step log data
	LogKindData LogKind = "data"
	// indicates start/end of a step
	LogKindControl LogKind = "control"
)

type LogLine struct {
	Kind    LogKind `json:"kind"`
	Content string  `json:"content"`

	// fields if kind is "data"
	Stream string `json:"stream,omitempty"`

	// fields if kind is "control"
	StepId      int      `json:"step_id,omitempty"`
	StepKind    StepKind `json:"step_kind,omitempty"`
	StepCommand string   `json:"step_command,omitempty"`
}

func NewDataLogLine(content, stream string) LogLine {
	return LogLine{
		Kind:    LogKindData,
		Content: content,
		Stream:  stream,
	}
}

func NewControlLogLine(idx int, step Step) LogLine {
	return LogLine{
		Kind:        LogKindControl,
		Content:     step.Name,
		StepId:      idx,
		StepKind:    step.Kind,
		StepCommand: step.Command,
	}
}
