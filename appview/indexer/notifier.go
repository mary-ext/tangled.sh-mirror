package indexer

import (
	"context"

	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/log"
)

var _ notify.Notifier = &Indexer{}

func (ix *Indexer) NewIssue(ctx context.Context, issue *models.Issue) {
	l := log.FromContext(ctx).With("notifier", "indexer.NewIssue", "issue", issue)
	l.Debug("indexing new issue")
	err := ix.Issues.Index(ctx, *issue)
	if err != nil {
		l.Error("failed to index an issue", "err", err)
	}
}
