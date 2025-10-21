package indexer

import (
	"context"

	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/log"
)

var _ notify.Notifier = &Indexer{}

func (ix *Indexer) NewIssue(ctx context.Context, issue *models.Issue) {
	l := log.FromContext(ctx).With("notifier", "indexer", "issue", issue)
	l.Debug("indexing new issue")
	err := ix.Issues.Index(ctx, *issue)
	if err != nil {
		l.Error("failed to index an issue", "err", err)
	}
}

func (ix *Indexer) NewIssueClosed(ctx context.Context, issue *models.Issue) {
	l := log.FromContext(ctx).With("notifier", "indexer", "issue", issue)
	l.Debug("updating an issue")
	err := ix.Issues.Index(ctx, *issue)
	if err != nil {
		l.Error("failed to index an issue", "err", err)
	}
}

func (ix *Indexer) DeleteIssue(ctx context.Context, issue *models.Issue) {
	l := log.FromContext(ctx).With("notifier", "indexer", "issue", issue)
	l.Debug("deleting an issue")
	err := ix.Issues.Delete(ctx, issue.Id)
	if err != nil {
		l.Error("failed to delete an issue", "err", err)
	}
}
