package workflow

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"tangled.org/core/api/tangled"
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

func TestCompileWorkflow_MultipleBranchAndTag(t *testing.T) {
	wf := Workflow{
		Name: ".tangled/workflows/branch_and_tag.yml",
		When: []Constraint{
			{
				Event:  []string{"push"},
				Branch: []string{"main", "develop"},
				Tag:    []string{"v*"},
			},
		},
		Engine: "nixery",
	}

	tests := []struct {
		name          string
		trigger       tangled.Pipeline_TriggerMetadata
		shouldMatch   bool
		expectedCount int
	}{
		{
			name: "matches main branch",
			trigger: tangled.Pipeline_TriggerMetadata{
				Kind: string(TriggerKindPush),
				Push: &tangled.Pipeline_PushTriggerData{
					Ref:    "refs/heads/main",
					OldSha: strings.Repeat("0", 40),
					NewSha: strings.Repeat("f", 40),
				},
			},
			shouldMatch:   true,
			expectedCount: 1,
		},
		{
			name: "matches develop branch",
			trigger: tangled.Pipeline_TriggerMetadata{
				Kind: string(TriggerKindPush),
				Push: &tangled.Pipeline_PushTriggerData{
					Ref:    "refs/heads/develop",
					OldSha: strings.Repeat("0", 40),
					NewSha: strings.Repeat("f", 40),
				},
			},
			shouldMatch:   true,
			expectedCount: 1,
		},
		{
			name: "matches v* tag pattern",
			trigger: tangled.Pipeline_TriggerMetadata{
				Kind: string(TriggerKindPush),
				Push: &tangled.Pipeline_PushTriggerData{
					Ref:    "refs/tags/v1.0.0",
					OldSha: strings.Repeat("0", 40),
					NewSha: strings.Repeat("f", 40),
				},
			},
			shouldMatch:   true,
			expectedCount: 1,
		},
		{
			name: "matches v* tag pattern with different version",
			trigger: tangled.Pipeline_TriggerMetadata{
				Kind: string(TriggerKindPush),
				Push: &tangled.Pipeline_PushTriggerData{
					Ref:    "refs/tags/v2.5.3",
					OldSha: strings.Repeat("0", 40),
					NewSha: strings.Repeat("f", 40),
				},
			},
			shouldMatch:   true,
			expectedCount: 1,
		},
		{
			name: "does not match master branch",
			trigger: tangled.Pipeline_TriggerMetadata{
				Kind: string(TriggerKindPush),
				Push: &tangled.Pipeline_PushTriggerData{
					Ref:    "refs/heads/master",
					OldSha: strings.Repeat("0", 40),
					NewSha: strings.Repeat("f", 40),
				},
			},
			shouldMatch:   false,
			expectedCount: 0,
		},
		{
			name: "does not match non-v tag",
			trigger: tangled.Pipeline_TriggerMetadata{
				Kind: string(TriggerKindPush),
				Push: &tangled.Pipeline_PushTriggerData{
					Ref:    "refs/tags/release-1.0",
					OldSha: strings.Repeat("0", 40),
					NewSha: strings.Repeat("f", 40),
				},
			},
			shouldMatch:   false,
			expectedCount: 0,
		},
		{
			name: "does not match feature branch",
			trigger: tangled.Pipeline_TriggerMetadata{
				Kind: string(TriggerKindPush),
				Push: &tangled.Pipeline_PushTriggerData{
					Ref:    "refs/heads/feature/new-feature",
					OldSha: strings.Repeat("0", 40),
					NewSha: strings.Repeat("f", 40),
				},
			},
			shouldMatch:   false,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Compiler{Trigger: tt.trigger}
			cp := c.Compile([]Workflow{wf})

			assert.Len(t, cp.Workflows, tt.expectedCount)
			if tt.shouldMatch {
				assert.Equal(t, wf.Name, cp.Workflows[0].Name)
			}
		})
	}
}
