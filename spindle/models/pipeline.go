package models

import (
	"path"

	"tangled.sh/tangled.sh/core/api/tangled"
)

type Pipeline struct {
	Workflows []Workflow
}

type Step struct {
	Command     string
	Name        string
	Environment map[string]string
}

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
func ToPipeline(pl tangled.Pipeline, dev bool) *Pipeline {
	workflows := []Workflow{}

	for _, twf := range pl.Workflows {
		swf := &Workflow{}
		for _, tstep := range twf.Steps {
			sstep := Step{}
			sstep.Environment = stepEnvToMap(tstep.Environment)
			sstep.Command = tstep.Command
			sstep.Name = tstep.Name
			swf.Steps = append(swf.Steps, sstep)
		}
		swf.Name = twf.Name
		swf.Environment = workflowEnvToMap(twf.Environment)
		swf.Image = workflowImage(twf.Dependencies)

		swf.addNixProfileToPath()
		setup := &setupSteps{}

		setup.addStep(cloneStep(*twf, *pl.TriggerMetadata.Repo, dev))
		setup.addStep(checkoutStep(*twf, *pl.TriggerMetadata))
		setup.addStep(dependencyStep(*twf))

		// append setup steps in order to the start of workflow steps
		swf.Steps = append(*setup, swf.Steps...)

		workflows = append(workflows, *swf)
	}
	return &Pipeline{Workflows: workflows}
}

func workflowEnvToMap(envs []*tangled.Pipeline_Workflow_Environment_Elem) map[string]string {
	envMap := map[string]string{}
	for _, env := range envs {
		envMap[env.Key] = env.Value
	}
	return envMap
}

func stepEnvToMap(envs []*tangled.Pipeline_Step_Environment_Elem) map[string]string {
	envMap := map[string]string{}
	for _, env := range envs {
		envMap[env.Key] = env.Value
	}
	return envMap
}

func workflowImage(deps []tangled.Pipeline_Dependencies_Elem) string {
	var dependencies string
	for _, d := range deps {
		if d.Registry == "nixpkgs" {
			dependencies = path.Join(d.Packages...)
		}
	}

	// load defaults from somewhere else
	dependencies = path.Join(dependencies, "bash", "git", "coreutils", "nix")

	// TODO: this should use nixery from the config
	return path.Join("nixery.dev", dependencies)
}

func (wf *Workflow) addNixProfileToPath() {
	wf.Environment["PATH"] = "$PATH:/.nix-profile/bin"
}
