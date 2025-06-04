package workflow

import (
	"fmt"

	"tangled.sh/tangled.sh/core/api/tangled"
)

type Compiler struct {
	Trigger     tangled.Pipeline_TriggerMetadata
	Diagnostics Diagnostics
}

type Diagnostics struct {
	Errors   []error
	Warnings []Warning
}

func (d *Diagnostics) Combine(o Diagnostics) {
	d.Errors = append(d.Errors, o.Errors...)
	d.Warnings = append(d.Warnings, o.Warnings...)
}

func (d *Diagnostics) AddWarning(path string, kind WarningKind, reason string) {
	d.Warnings = append(d.Warnings, Warning{path, kind, reason})
}

func (d *Diagnostics) AddError(err error) {
	d.Errors = append(d.Errors, err)
}

func (d Diagnostics) IsErr() bool {
	return len(d.Errors) != 0
}

type Warning struct {
	Path   string
	Type   WarningKind
	Reason string
}

type WarningKind string

var (
	WorkflowSkipped      WarningKind = "workflow skipped"
	InvalidConfiguration WarningKind = "invalid configuration"
)

// convert a repositories' workflow files into a fully compiled pipeline that runners accept
func (compiler *Compiler) Compile(p Pipeline) tangled.Pipeline {
	cp := tangled.Pipeline{
		TriggerMetadata: &compiler.Trigger,
	}

	for _, w := range p {
		cw := compiler.compileWorkflow(w)

		// empty workflows are not added to the pipeline
		if len(cw.Steps) == 0 {
			continue
		}

		cp.Workflows = append(cp.Workflows, &cw)
	}

	return cp
}

func (compiler *Compiler) compileWorkflow(w Workflow) tangled.Pipeline_Workflow {
	cw := tangled.Pipeline_Workflow{}

	if !w.Match(compiler.Trigger) {
		compiler.Diagnostics.AddWarning(
			w.Name,
			WorkflowSkipped,
			fmt.Sprintf("did not match trigger %s", compiler.Trigger.Kind),
		)
		return cw
	}

	if len(w.Steps) == 0 {
		compiler.Diagnostics.AddWarning(
			w.Name,
			WorkflowSkipped,
			"empty workflow",
		)
		return cw
	}

	// validate clone options
	compiler.analyzeCloneOptions(w)

	cw.Name = w.Name
	cw.Dependencies = w.Dependencies.AsRecord()
	for _, s := range w.Steps {
		step := tangled.Pipeline_Step{
			Command: s.Command,
			Name:    s.Name,
		}
		cw.Steps = append(cw.Steps, &step)
	}
	for k, v := range w.Environment {
		e := &tangled.Pipeline_Workflow_Environment_Elem{
			Key:   k,
			Value: v,
		}
		cw.Environment = append(cw.Environment, e)
	}

	o := w.CloneOpts.AsRecord()
	cw.Clone = &o

	return cw
}

func (compiler *Compiler) analyzeCloneOptions(w Workflow) {
	if w.CloneOpts.Skip && w.CloneOpts.IncludeSubmodules {
		compiler.Diagnostics.AddWarning(
			w.Name,
			InvalidConfiguration,
			"cannot apply `clone.skip` and `clone.submodules`",
		)
	}

	if w.CloneOpts.Skip && w.CloneOpts.Depth > 0 {
		compiler.Diagnostics.AddWarning(
			w.Name,
			InvalidConfiguration,
			"cannot apply `clone.skip` and `clone.depth`",
		)
	}
}
