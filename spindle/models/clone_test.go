package models

import (
	"strings"
	"testing"

	"tangled.org/core/api/tangled"
	"tangled.org/core/workflow"
)

func TestBuildCloneStep_PushTrigger(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth:      1,
			Submodules: false,
			Skip:       false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindPush),
		Push: &tangled.Pipeline_PushTriggerData{
			NewSha: "abc123",
			OldSha: "def456",
			Ref:    "refs/heads/main",
		},
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	if step.Kind() != StepKindSystem {
		t.Errorf("Expected StepKindSystem, got %v", step.Kind())
	}

	if step.Name() != "Clone repository into workspace" {
		t.Errorf("Expected 'Clone repository into workspace', got '%s'", step.Name())
	}

	commands := step.Commands()
	if len(commands) != 4 {
		t.Errorf("Expected 4 commands, got %d", len(commands))
	}

	// Verify commands contain expected git operations
	allCmds := strings.Join(commands, " ")
	if !strings.Contains(allCmds, "git init") {
		t.Error("Commands should contain 'git init'")
	}
	if !strings.Contains(allCmds, "git remote add origin") {
		t.Error("Commands should contain 'git remote add origin'")
	}
	if !strings.Contains(allCmds, "git fetch") {
		t.Error("Commands should contain 'git fetch'")
	}
	if !strings.Contains(allCmds, "abc123") {
		t.Error("Commands should contain commit SHA")
	}
	if !strings.Contains(allCmds, "git checkout FETCH_HEAD") {
		t.Error("Commands should contain 'git checkout FETCH_HEAD'")
	}
	if !strings.Contains(allCmds, "https://example.com/did:plc:user123/my-repo") {
		t.Error("Commands should contain expected repo URL")
	}
}

func TestBuildCloneStep_PullRequestTrigger(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth: 1,
			Skip:  false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindPullRequest),
		PullRequest: &tangled.Pipeline_PullRequestTriggerData{
			SourceSha:    "pr-sha-789",
			SourceBranch: "feature-branch",
			TargetBranch: "main",
			Action:       "opened",
		},
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	allCmds := strings.Join(step.Commands(), " ")
	if !strings.Contains(allCmds, "pr-sha-789") {
		t.Error("Commands should contain PR commit SHA")
	}
}

func TestBuildCloneStep_ManualTrigger(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth: 1,
			Skip:  false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindManual),
		Manual: &tangled.Pipeline_ManualTriggerData{
			Inputs: nil,
		},
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	// Manual triggers don't have a SHA yet (TODO), so git fetch won't include a SHA
	allCmds := strings.Join(step.Commands(), " ")
	// Should still have basic git commands
	if !strings.Contains(allCmds, "git init") {
		t.Error("Commands should contain 'git init'")
	}
	if !strings.Contains(allCmds, "git fetch") {
		t.Error("Commands should contain 'git fetch'")
	}
}

func TestBuildCloneStep_SkipFlag(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Skip: true,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindPush),
		Push: &tangled.Pipeline_PushTriggerData{
			NewSha: "abc123",
		},
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	// Empty step when skip is true
	if step.Name() != "" {
		t.Error("Expected empty step name when Skip is true")
	}
	if len(step.Commands()) != 0 {
		t.Errorf("Expected no commands when Skip is true, got %d commands", len(step.Commands()))
	}
}

func TestBuildCloneStep_DevMode(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth: 1,
			Skip:  false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindPush),
		Push: &tangled.Pipeline_PushTriggerData{
			NewSha: "abc123",
		},
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "localhost:3000",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, true)

	// In dev mode, should use http:// and replace localhost with host.docker.internal
	allCmds := strings.Join(step.Commands(), " ")
	expectedURL := "http://host.docker.internal:3000/did:plc:user123/my-repo"
	if !strings.Contains(allCmds, expectedURL) {
		t.Errorf("Expected dev mode URL '%s' in commands", expectedURL)
	}
}

func TestBuildCloneStep_DepthAndSubmodules(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth:      10,
			Submodules: true,
			Skip:       false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindPush),
		Push: &tangled.Pipeline_PushTriggerData{
			NewSha: "abc123",
		},
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	allCmds := strings.Join(step.Commands(), " ")
	if !strings.Contains(allCmds, "--depth=10") {
		t.Error("Commands should contain '--depth=10'")
	}

	if !strings.Contains(allCmds, "--recurse-submodules=yes") {
		t.Error("Commands should contain '--recurse-submodules=yes'")
	}
}

func TestBuildCloneStep_DefaultDepth(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth: 0, // Default should be 1
			Skip:  false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindPush),
		Push: &tangled.Pipeline_PushTriggerData{
			NewSha: "abc123",
		},
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	allCmds := strings.Join(step.Commands(), " ")
	if !strings.Contains(allCmds, "--depth=1") {
		t.Error("Commands should default to '--depth=1'")
	}
}

func TestBuildCloneStep_NilPushData(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth: 1,
			Skip:  false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindPush),
		Push: nil, // Nil push data should create error step
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	// Should return an error step
	if !strings.Contains(step.Name(), "error") {
		t.Error("Expected error in step name when push data is nil")
	}

	allCmds := strings.Join(step.Commands(), " ")
	if !strings.Contains(allCmds, "Failed to get clone info") {
		t.Error("Commands should contain error message")
	}
	if !strings.Contains(allCmds, "exit 1") {
		t.Error("Commands should exit with error")
	}
}

func TestBuildCloneStep_NilPRData(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth: 1,
			Skip:  false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind:        string(workflow.TriggerKindPullRequest),
		PullRequest: nil, // Nil PR data should create error step
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	// Should return an error step
	if !strings.Contains(step.Name(), "error") {
		t.Error("Expected error in step name when pull request data is nil")
	}

	allCmds := strings.Join(step.Commands(), " ")
	if !strings.Contains(allCmds, "Failed to get clone info") {
		t.Error("Commands should contain error message")
	}
}

func TestBuildCloneStep_UnknownTriggerKind(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: &tangled.Pipeline_CloneOpts{
			Depth: 1,
			Skip:  false,
		},
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: "unknown_trigger",
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	// Should return an error step
	if !strings.Contains(step.Name(), "error") {
		t.Error("Expected error in step name for unknown trigger kind")
	}

	allCmds := strings.Join(step.Commands(), " ")
	if !strings.Contains(allCmds, "unknown trigger kind") {
		t.Error("Commands should contain error message about unknown trigger kind")
	}
}

func TestBuildCloneStep_NilCloneOpts(t *testing.T) {
	twf := tangled.Pipeline_Workflow{
		Clone: nil, // Nil clone options should use defaults
	}
	tr := tangled.Pipeline_TriggerMetadata{
		Kind: string(workflow.TriggerKindPush),
		Push: &tangled.Pipeline_PushTriggerData{
			NewSha: "abc123",
		},
		Repo: &tangled.Pipeline_TriggerRepo{
			Knot: "example.com",
			Did:  "did:plc:user123",
			Repo: "my-repo",
		},
	}

	step := BuildCloneStep(twf, tr, false)

	// Should still work with default options
	if step.Kind() != StepKindSystem {
		t.Errorf("Expected StepKindSystem, got %v", step.Kind())
	}

	allCmds := strings.Join(step.Commands(), " ")
	if !strings.Contains(allCmds, "--depth=1") {
		t.Error("Commands should default to '--depth=1' when Clone is nil")
	}
	if !strings.Contains(allCmds, "git init") {
		t.Error("Commands should contain 'git init'")
	}
}
