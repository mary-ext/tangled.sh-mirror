package workflow

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"tangled.org/core/api/tangled"

	"github.com/go-git/go-git/v5/plumbing"
	"gopkg.in/yaml.v3"
)

// - when a repo is modified, it results in the trigger of a "Pipeline"
// - a repo could consist of several workflow files
//   * .tangled/workflows/test.yml
//   * .tangled/workflows/lint.yml
// - therefore a pipeline consists of several workflows, these execute in parallel
// - each workflow consists of some execution steps, these execute serially

type (
	Pipeline []Workflow

	// this is simply a structural representation of the workflow file
	Workflow struct {
		Name      string       `yaml:"-"` // name of the workflow file
		Engine    string       `yaml:"engine"`
		When      []Constraint `yaml:"when"`
		CloneOpts CloneOpts    `yaml:"clone"`
		Raw       string       `yaml:"-"`
	}

	Constraint struct {
		Event  StringList `yaml:"event"`
		Branch StringList `yaml:"branch"` // this is optional, and only applied on "push" events
	}

	CloneOpts struct {
		Skip              bool `yaml:"skip"`
		Depth             int  `yaml:"depth"`
		IncludeSubmodules bool `yaml:"submodules"`
	}

	StringList []string

	TriggerKind string
)

const (
	WorkflowDir = ".tangled/workflows"

	TriggerKindPush        TriggerKind = "push"
	TriggerKindPullRequest TriggerKind = "pull_request"
	TriggerKindManual      TriggerKind = "manual"
)

func (t TriggerKind) String() string {
	return strings.ReplaceAll(string(t), "_", " ")
}

func FromFile(name string, contents []byte) (Workflow, error) {
	var wf Workflow

	err := yaml.Unmarshal(contents, &wf)
	if err != nil {
		return wf, err
	}

	wf.Name = name
	wf.Raw = string(contents)

	return wf, nil
}

// if any of the constraints on a workflow is true, return true
func (w *Workflow) Match(trigger tangled.Pipeline_TriggerMetadata) bool {
	// manual triggers always run the workflow
	if trigger.Manual != nil {
		return true
	}

	// if not manual, run through the constraint list and see if any one matches
	for _, c := range w.When {
		if c.Match(trigger) {
			return true
		}
	}

	// no constraints, always run this workflow
	if len(w.When) == 0 {
		return true
	}

	return false
}

func (c *Constraint) Match(trigger tangled.Pipeline_TriggerMetadata) bool {
	match := true

	// manual triggers always pass this constraint
	if trigger.Manual != nil {
		return true
	}

	// apply event constraints
	match = match && c.MatchEvent(trigger.Kind)

	// apply branch constraints for PRs
	if trigger.PullRequest != nil {
		match = match && c.MatchBranch(trigger.PullRequest.TargetBranch)
	}

	// apply ref constraints for pushes
	if trigger.Push != nil {
		match = match && c.MatchRef(trigger.Push.Ref)
	}

	return match
}

func (c *Constraint) MatchBranch(branch string) bool {
	return slices.Contains(c.Branch, branch)
}

func (c *Constraint) MatchRef(ref string) bool {
	refName := plumbing.ReferenceName(ref)
	if refName.IsBranch() {
		return slices.Contains(c.Branch, refName.Short())
	}
	return false
}

func (c *Constraint) MatchEvent(event string) bool {
	return slices.Contains(c.Event, event)
}

// Custom unmarshaller for StringList
func (s *StringList) UnmarshalYAML(unmarshal func(any) error) error {
	var stringType string
	if err := unmarshal(&stringType); err == nil {
		*s = []string{stringType}
		return nil
	}

	var sliceType []any
	if err := unmarshal(&sliceType); err == nil {

		if sliceType == nil {
			*s = nil
			return nil
		}

		parts := make([]string, len(sliceType))
		for k, v := range sliceType {
			if sv, ok := v.(string); ok {
				parts[k] = sv
			} else {
				return fmt.Errorf("cannot unmarshal '%v' of type %T into a string value", v, v)
			}
		}

		*s = parts
		return nil
	}

	return errors.New("failed to unmarshal StringOrSlice")
}

func (c CloneOpts) AsRecord() tangled.Pipeline_CloneOpts {
	return tangled.Pipeline_CloneOpts{
		Depth:      int64(c.Depth),
		Skip:       c.Skip,
		Submodules: c.IncludeSubmodules,
	}
}
