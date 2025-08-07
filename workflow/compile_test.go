package workflow

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"tangled.sh/tangled.sh/core/api/tangled"
)

var trigger = tangled.Pipeline_TriggerMetadata{
	Kind: string(TriggerKindPush),
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
		Name:      ".tangled/workflows/test.yml",
		Engine:    "nixery",
		When:      when,
		CloneOpts: CloneOpts{}, // default true
	}

	c := Compiler{Trigger: trigger}
	cp := c.Compile([]Workflow{wf})

	assert.Len(t, cp.Workflows, 1)
	assert.Equal(t, wf.Name, cp.Workflows[0].Name)
	assert.False(t, cp.Workflows[0].Clone.Skip)
	assert.False(t, c.Diagnostics.IsErr())
}

func TestCompileWorkflow_TriggerMismatch(t *testing.T) {
	wf := Workflow{
		Name:   ".tangled/workflows/mismatch.yml",
		Engine: "nixery",
		When: []Constraint{
			{
				Event:  []string{"push"},
				Branch: []string{"master"}, // different branch
			},
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
		Name:   ".tangled/workflows/clone_skip.yml",
		Engine: "nixery",
		When:   when,
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

func TestCompileWorkflow_MissingEngine(t *testing.T) {
	wf := Workflow{
		Name:   ".tangled/workflows/missing_engine.yml",
		When:   when,
		Engine: "",
	}

	c := Compiler{Trigger: trigger}
	cp := c.Compile([]Workflow{wf})

	assert.Len(t, cp.Workflows, 0)
	assert.Len(t, c.Diagnostics.Errors, 1)
	assert.Equal(t, MissingEngine, c.Diagnostics.Errors[0].Error)
}
