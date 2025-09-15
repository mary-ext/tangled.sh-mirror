package notify

import (
	"context"

	"tangled.org/core/appview/models"
)

type mergedNotifier struct {
	notifiers []Notifier
}

func NewMergedNotifier(notifiers ...Notifier) Notifier {
	return &mergedNotifier{notifiers}
}

var _ Notifier = &mergedNotifier{}

func (m *mergedNotifier) NewRepo(ctx context.Context, repo *models.Repo) {
	for _, notifier := range m.notifiers {
		notifier.NewRepo(ctx, repo)
	}
}

func (m *mergedNotifier) NewStar(ctx context.Context, star *models.Star) {
	for _, notifier := range m.notifiers {
		notifier.NewStar(ctx, star)
	}
}
func (m *mergedNotifier) DeleteStar(ctx context.Context, star *models.Star) {
	for _, notifier := range m.notifiers {
		notifier.DeleteStar(ctx, star)
	}
}

func (m *mergedNotifier) NewIssue(ctx context.Context, issue *models.Issue) {
	for _, notifier := range m.notifiers {
		notifier.NewIssue(ctx, issue)
	}
}
func (m *mergedNotifier) NewIssueComment(ctx context.Context, comment *models.IssueComment) {
	for _, notifier := range m.notifiers {
		notifier.NewIssueComment(ctx, comment)
	}
}

func (m *mergedNotifier) NewIssueClosed(ctx context.Context, issue *models.Issue) {
	for _, notifier := range m.notifiers {
		notifier.NewIssueClosed(ctx, issue)
	}
}

func (m *mergedNotifier) NewFollow(ctx context.Context, follow *models.Follow) {
	for _, notifier := range m.notifiers {
		notifier.NewFollow(ctx, follow)
	}
}
func (m *mergedNotifier) DeleteFollow(ctx context.Context, follow *models.Follow) {
	for _, notifier := range m.notifiers {
		notifier.DeleteFollow(ctx, follow)
	}
}

func (m *mergedNotifier) NewPull(ctx context.Context, pull *models.Pull) {
	for _, notifier := range m.notifiers {
		notifier.NewPull(ctx, pull)
	}
}
func (m *mergedNotifier) NewPullComment(ctx context.Context, comment *models.PullComment) {
	for _, notifier := range m.notifiers {
		notifier.NewPullComment(ctx, comment)
	}
}

func (m *mergedNotifier) NewPullMerged(ctx context.Context, pull *models.Pull) {
	for _, notifier := range m.notifiers {
		notifier.NewPullMerged(ctx, pull)
	}
}

func (m *mergedNotifier) NewPullClosed(ctx context.Context, pull *models.Pull) {
	for _, notifier := range m.notifiers {
		notifier.NewPullClosed(ctx, pull)
	}
}

func (m *mergedNotifier) UpdateProfile(ctx context.Context, profile *models.Profile) {
	for _, notifier := range m.notifiers {
		notifier.UpdateProfile(ctx, profile)
	}
}

func (m *mergedNotifier) NewString(ctx context.Context, string *models.String) {
	for _, notifier := range m.notifiers {
		notifier.NewString(ctx, string)
	}
}

func (m *mergedNotifier) EditString(ctx context.Context, string *models.String) {
	for _, notifier := range m.notifiers {
		notifier.EditString(ctx, string)
	}
}

func (m *mergedNotifier) DeleteString(ctx context.Context, did, rkey string) {
	for _, notifier := range m.notifiers {
		notifier.DeleteString(ctx, did, rkey)
	}
}
