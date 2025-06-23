package engine

import "errors"

var (
	ErrOOMKilled      = errors.New("oom killed")
	ErrTimedOut       = errors.New("timed out")
	ErrWorkflowFailed = errors.New("workflow failed")
)
