package models

type Pipeline struct {
	RepoOwner string
	RepoName  string
	Workflows map[Engine][]Workflow
}

type Step interface {
	Name() string
	Command() string
	Kind() StepKind
}

type StepKind int

const (
	// steps injected by the CI runner
	StepKindSystem StepKind = iota
	// steps defined by the user in the original pipeline
	StepKindUser
)

type Workflow struct {
	Steps []Step
	Name  string
	Data  any
}
