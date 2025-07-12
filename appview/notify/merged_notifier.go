package notify

import (
	"context"

	"tangled.sh/tangled.sh/core/appview/db"
)

type mergedNotifier struct {
	notifiers []Notifier
}

func NewMergedNotifier(notifiers ...Notifier) Notifier {
	return &mergedNotifier{
		notifiers,
	}
}

var _ Notifier = &mergedNotifier{}

func (m *mergedNotifier) NewIssue(ctx context.Context, issue *db.Issue) {
	for _, notifier := range m.notifiers {
		notifier.NewIssue(ctx, issue)
	}
}

func (m *mergedNotifier) NewIssueComment(ctx context.Context, comment db.Comment) {
	for _, notifier := range m.notifiers {
		notifier.NewIssueComment(ctx, comment)
	}
}

func (m *mergedNotifier) NewPullComment(ctx context.Context, comment db.PullComment) {
	for _, notifier := range m.notifiers {
		notifier.NewPullComment(ctx, comment)
	}
}
