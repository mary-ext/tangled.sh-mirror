package workflow

import (
	"errors"
	"fmt"

	"tangled.org/core/api/tangled"
)

type RawWorkflow struct {
	Name     string
	Contents []byte
}

type RawPipeline = []RawWorkflow

type Compiler struct {
	Trigger     tangled.Pipeline_TriggerMetadata
	Diagnostics Diagnostics
}

type Diagnostics struct {
	Errors   []Error
	Warnings []Warning
}

func (d *Diagnostics) IsEmpty() bool {
	return len(d.Errors) == 0 && len(d.Warnings) == 0
}

func (d *Diagnostics) Combine(o Diagnostics) {
	d.Errors = append(d.Errors, o.Errors...)
	d.Warnings = append(d.Warnings, o.Warnings...)
}

func (d *Diagnostics) AddWarning(path string, kind WarningKind, reason string) {
	d.Warnings = append(d.Warnings, Warning{path, kind, reason})
}

func (d *Diagnostics) AddError(path string, err error) {
	d.Errors = append(d.Errors, Error{path, err})
}

func (d Diagnostics) IsErr() bool {
	return len(d.Errors) != 0
}

type Error struct {
	Path  string
	Error error
}

func (e Error) String() string {
	return fmt.Sprintf("error: %s: %s", e.Path, e.Error.Error())
}

type Warning struct {
	Path   string
	Type   WarningKind
	Reason string
}

func (w Warning) String() string {
	return fmt.Sprintf("warning: %s: %s: %s", w.Path, w.Type, w.Reason)
}

var (
	MissingEngine error = errors.New("missing engine")
)

type WarningKind string

var (
	WorkflowSkipped      WarningKind = "workflow skipped"
	InvalidConfiguration WarningKind = "invalid configuration"
)

func (compiler *Compiler) Parse(p RawPipeline) Pipeline {
	var pp Pipeline

	for _, w := range p {
		wf, err := FromFile(w.Name, w.Contents)
		if err != nil {
			compiler.Diagnostics.AddError(w.Name, err)
			continue
		}

		pp = append(pp, wf)
	}

	return pp
}

// convert a repositories' workflow files into a fully compiled pipeline that runners accept
func (compiler *Compiler) Compile(p Pipeline) tangled.Pipeline {
	cp := tangled.Pipeline{
		TriggerMetadata: &compiler.Trigger,
	}

	for _, wf := range p {
		cw := compiler.compileWorkflow(wf)

		if cw == nil {
			continue
		}

		cp.Workflows = append(cp.Workflows, cw)
	}

	return cp
}

func (compiler *Compiler) compileWorkflow(w Workflow) *tangled.Pipeline_Workflow {
	cw := &tangled.Pipeline_Workflow{}

	matched, err := w.Match(compiler.Trigger)
	if err != nil {
		compiler.Diagnostics.AddError(
			w.Name,
			fmt.Errorf("failed to execute workflow: %w", err),
		)
		return nil
	}
	if !matched {
		compiler.Diagnostics.AddWarning(
			w.Name,
			WorkflowSkipped,
			fmt.Sprintf("did not match trigger %s", compiler.Trigger.Kind),
		)
		return nil
	}

	// validate clone options
	compiler.analyzeCloneOptions(w)

	cw.Name = w.Name

	if w.Engine == "" {
		compiler.Diagnostics.AddError(w.Name, MissingEngine)
		return nil
	}

	cw.Engine = w.Engine
	cw.Raw = w.Raw

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
