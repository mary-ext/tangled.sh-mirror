package workflow

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"tangled.sh/tangled.sh/core/api/tangled"
)

var trigger = tangled.Pipeline_TriggerMetadata{
	Kind: TriggerKindPush,
	Push: &tangled.Pipeline_PushTriggerData{
		Ref:    "refs/heads/main",
		OldSha: strings.Repeat("0", 40),
		NewSha: strings.Repeat("f", 40),
	},
}

var when = []Constraint{
	{
		Event:  []string{"push"},
		Branch: []string{"main"},
	},
}

func TestCompileWorkflow_MatchingWorkflowWithSteps(t *testing.T) {
	wf := Workflow{
		Name: ".tangled/workflows/test.yml",
		When: when,
		Steps: []Step{
			{Name: "Test", Command: "go test ./..."},
		},
		CloneOpts: CloneOpts{}, // default true
	}

	c := Compiler{Trigger: trigger}
	cp := c.Compile([]Workflow{wf})

	assert.Len(t, cp.Workflows, 1)
	assert.Equal(t, wf.Name, cp.Workflows[0].Name)
	assert.False(t, cp.Workflows[0].Clone.Skip)
	assert.False(t, c.Diagnostics.IsErr())
}

func TestCompileWorkflow_EmptySteps(t *testing.T) {
	wf := Workflow{
		Name:  ".tangled/workflows/empty.yml",
		When:  when,
		Steps: []Step{}, // no steps
	}

	c := Compiler{Trigger: trigger}
	cp := c.Compile([]Workflow{wf})

	assert.Len(t, cp.Workflows, 0)
	assert.Len(t, c.Diagnostics.Warnings, 1)
	assert.Equal(t, WorkflowSkipped, c.Diagnostics.Warnings[0].Type)
}

func TestCompileWorkflow_TriggerMismatch(t *testing.T) {
	wf := Workflow{
		Name: ".tangled/workflows/mismatch.yml",
		When: []Constraint{
			{
				Event:  []string{"push"},
				Branch: []string{"master"}, // different branch
			},
		},
		Steps: []Step{
			{Name: "Lint", Command: "golint ./..."},
		},
	}

	c := Compiler{Trigger: trigger}
	cp := c.Compile([]Workflow{wf})

	assert.Len(t, cp.Workflows, 0)
	assert.Len(t, c.Diagnostics.Warnings, 1)
	assert.Equal(t, WorkflowSkipped, c.Diagnostics.Warnings[0].Type)
}

func TestCompileWorkflow_CloneFalseWithShallowTrue(t *testing.T) {
	wf := Workflow{
		Name: ".tangled/workflows/clone_skip.yml",
		When: when,
		Steps: []Step{
			{Name: "Skip", Command: "echo skip"},
		},
		CloneOpts: CloneOpts{
			Skip:  true,
			Depth: 1,
		}, // false
	}

	c := Compiler{Trigger: trigger}
	cp := c.Compile([]Workflow{wf})

	assert.Len(t, cp.Workflows, 1)
	assert.True(t, cp.Workflows[0].Clone.Skip)
	assert.Len(t, c.Diagnostics.Warnings, 1)
	assert.Equal(t, InvalidConfiguration, c.Diagnostics.Warnings[0].Type)
}
