package models

import (
	"fmt"
	"strings"

	"tangled.org/core/api/tangled"
	"tangled.org/core/workflow"
)

type CloneStep struct {
	name     string
	kind     StepKind
	commands []string
}

func (s CloneStep) Name() string {
	return s.name
}

func (s CloneStep) Commands() []string {
	return s.commands
}

func (s CloneStep) Command() string {
	return strings.Join(s.commands, "\n")
}

func (s CloneStep) Kind() StepKind {
	return s.kind
}

// BuildCloneStep generates git clone commands.
// The caller must ensure the current working directory is set to the desired
// workspace directory before executing these commands.
//
// The generated commands are:
// - git init
// - git remote add origin <url>
// - git fetch --depth=<d> --recurse-submodules=<yes|no> <sha>
// - git checkout FETCH_HEAD
//
// Supports all trigger types (push, PR, manual) and clone options.
func BuildCloneStep(twf tangled.Pipeline_Workflow, tr tangled.Pipeline_TriggerMetadata, dev bool) CloneStep {
	if twf.Clone != nil && twf.Clone.Skip {
		return CloneStep{}
	}

	commitSHA, err := extractCommitSHA(tr)
	if err != nil {
		return CloneStep{
			kind:     StepKindSystem,
			name:     "Clone repository into workspace (error)",
			commands: []string{fmt.Sprintf("echo 'Failed to get clone info: %s' && exit 1", err.Error())},
		}
	}

	repoURL := buildRepoURL(tr, dev)

	var cloneOpts tangled.Pipeline_CloneOpts
	if twf.Clone != nil {
		cloneOpts = *twf.Clone
	}
	fetchArgs := buildFetchArgs(cloneOpts, commitSHA)

	return CloneStep{
		kind: StepKindSystem,
		name: "Clone repository into workspace",
		commands: []string{
			"git init",
			fmt.Sprintf("git remote add origin %s", repoURL),
			fmt.Sprintf("git fetch %s", strings.Join(fetchArgs, " ")),
			"git checkout FETCH_HEAD",
		},
	}
}

// extractCommitSHA extracts the commit SHA from trigger metadata based on trigger type
func extractCommitSHA(tr tangled.Pipeline_TriggerMetadata) (string, error) {
	switch workflow.TriggerKind(tr.Kind) {
	case workflow.TriggerKindPush:
		if tr.Push == nil {
			return "", fmt.Errorf("push trigger metadata is nil")
		}
		return tr.Push.NewSha, nil

	case workflow.TriggerKindPullRequest:
		if tr.PullRequest == nil {
			return "", fmt.Errorf("pull request trigger metadata is nil")
		}
		return tr.PullRequest.SourceSha, nil

	case workflow.TriggerKindManual:
		// Manual triggers don't have an explicit SHA in the metadata
		// For now, return empty string - could be enhanced to fetch from default branch
		// TODO: Implement manual trigger SHA resolution (fetch default branch HEAD)
		return "", nil

	default:
		return "", fmt.Errorf("unknown trigger kind: %s", tr.Kind)
	}
}

// buildRepoURL constructs the repository URL from trigger metadata
func buildRepoURL(tr tangled.Pipeline_TriggerMetadata, devMode bool) string {
	if tr.Repo == nil {
		return ""
	}

	// Determine protocol
	scheme := "https://"
	if devMode {
		scheme = "http://"
	}

	// Get host from knot
	host := tr.Repo.Knot

	// In dev mode, replace localhost with host.docker.internal for Docker networking
	if devMode && strings.Contains(host, "localhost") {
		host = strings.ReplaceAll(host, "localhost", "host.docker.internal")
	}

	// Build URL: {scheme}{knot}/{did}/{repo}
	return fmt.Sprintf("%s%s/%s/%s", scheme, host, tr.Repo.Did, tr.Repo.Repo)
}

// buildFetchArgs constructs the arguments for git fetch based on clone options
func buildFetchArgs(clone tangled.Pipeline_CloneOpts, sha string) []string {
	args := []string{}

	// Set fetch depth (default to 1 for shallow clone)
	depth := clone.Depth
	if depth == 0 {
		depth = 1
	}
	args = append(args, fmt.Sprintf("--depth=%d", depth))

	// Add submodules if requested
	if clone.Submodules {
		args = append(args, "--recurse-submodules=yes")
	}

	// Add remote and SHA
	args = append(args, "origin")
	if sha != "" {
		args = append(args, sha)
	}

	return args
}
