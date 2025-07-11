package models

import (
	"path"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/spindle/config"
)

type Pipeline struct {
	Workflows []Workflow
}

type Step struct {
	Command     string
	Name        string
	Environment map[string]string
	Kind        StepKind
}

type StepKind int

const (
	// steps injected by the CI runner
	StepKindSystem StepKind = iota
	// steps defined by the user in the original pipeline
	StepKindUser
)

type Workflow struct {
	Steps       []Step
	Environment map[string]string
	Name        string
	Image       string
}

// setupSteps get added to start of Steps
type setupSteps []Step

// addStep adds a step to the beginning of the workflow's steps.
func (ss *setupSteps) addStep(step Step) {
	*ss = append(*ss, step)
}

// ToPipeline converts a tangled.Pipeline into a model.Pipeline.
// In the process, dependencies are resolved: nixpkgs deps
// are constructed atop nixery and set as the Workflow.Image,
// and ones from custom registries
func ToPipeline(pl tangled.Pipeline, cfg config.Config) *Pipeline {
	workflows := []Workflow{}

	for _, twf := range pl.Workflows {
		swf := &Workflow{}
		for _, tstep := range twf.Steps {
			sstep := Step{}
			sstep.Environment = stepEnvToMap(tstep.Environment)
			sstep.Command = tstep.Command
			sstep.Name = tstep.Name
			sstep.Kind = StepKindUser
			swf.Steps = append(swf.Steps, sstep)
		}
		swf.Name = twf.Name
		swf.Environment = workflowEnvToMap(twf.Environment)
		swf.Image = workflowImage(twf.Dependencies, cfg.Pipelines.Nixery)

		swf.addNixProfileToPath()
		swf.setGlobalEnvs()
		setup := &setupSteps{}

		setup.addStep(nixConfStep())
		setup.addStep(cloneStep(*twf, *pl.TriggerMetadata, cfg.Server.Dev))
		setup.addStep(checkoutStep(*twf, *pl.TriggerMetadata))
		// this step could be empty
		if s := dependencyStep(*twf); s != nil {
			setup.addStep(*s)
		}

		// append setup steps in order to the start of workflow steps
		swf.Steps = append(*setup, swf.Steps...)

		workflows = append(workflows, *swf)
	}
	return &Pipeline{Workflows: workflows}
}

func workflowEnvToMap(envs []*tangled.Pipeline_Pair) map[string]string {
	envMap := map[string]string{}
	for _, env := range envs {
		if env != nil {
			envMap[env.Key] = env.Value
		}
	}
	return envMap
}

func stepEnvToMap(envs []*tangled.Pipeline_Pair) map[string]string {
	envMap := map[string]string{}
	for _, env := range envs {
		if env != nil {
			envMap[env.Key] = env.Value
		}
	}
	return envMap
}

func workflowImage(deps []*tangled.Pipeline_Dependency, nixery string) string {
	var dependencies string
	for _, d := range deps {
		if d.Registry == "nixpkgs" {
			dependencies = path.Join(d.Packages...)
		}
	}

	// load defaults from somewhere else
	dependencies = path.Join(dependencies, "bash", "git", "coreutils", "nix")

	return path.Join(nixery, dependencies)
}

func (wf *Workflow) addNixProfileToPath() {
	wf.Environment["PATH"] = "$PATH:/.nix-profile/bin"
}

func (wf *Workflow) setGlobalEnvs() {
	wf.Environment["NIX_CONFIG"] = "experimental-features = nix-command flakes"
	wf.Environment["HOME"] = "/tangled/workspace"
}
