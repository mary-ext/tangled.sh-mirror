package models

import (
	"context"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/spindle/secrets"
)

type Engine interface {
	InitWorkflow(twf tangled.Pipeline_Workflow, tpl tangled.Pipeline) (*Workflow, error)
	SetupWorkflow(ctx context.Context, wid WorkflowId, wf *Workflow) error
	WorkflowTimeout() time.Duration
	DestroyWorkflow(ctx context.Context, wid WorkflowId) error
	RunStep(ctx context.Context, wid WorkflowId, w *Workflow, idx int, secrets []secrets.UnlockedSecret, wfLogger *WorkflowLogger) error
}
