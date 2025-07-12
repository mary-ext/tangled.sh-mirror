package notify

import (
	"context"

	"tangled.sh/tangled.sh/core/appview/db"
)

type Notifier interface {
	NewIssue(ctx context.Context, issue *db.Issue)
	NewIssueComment(ctx context.Context, comment db.Comment)

	NewPullComment(ctx context.Context, comment db.PullComment)
}
