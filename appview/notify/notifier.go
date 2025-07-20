package notify

import (
	"context"

	"tangled.sh/tangled.sh/core/appview/db"
)

type Notifier interface {
	NewRepo(ctx context.Context, repo *db.Repo)

	NewStar(ctx context.Context, star *db.Star)
	DeleteStar(ctx context.Context, star *db.Star)

	NewIssue(ctx context.Context, issue *db.Issue)

	NewFollow(ctx context.Context, follow *db.Follow)
	DeleteFollow(ctx context.Context, follow *db.Follow)

	NewPull(ctx context.Context, pull *db.Pull)
	NewPullComment(ctx context.Context, comment *db.PullComment)

	UpdateProfile(ctx context.Context, profile *db.Profile)
}

// BaseNotifier is a listener that does nothing
type BaseNotifier struct{}

var _ Notifier = &BaseNotifier{}

func (m *BaseNotifier) NewRepo(ctx context.Context, repo *db.Repo) {}

func (m *BaseNotifier) NewStar(ctx context.Context, star *db.Star) {}
func (m *BaseNotifier) DeleteStar(ctx context.Context, star *db.Star) {}

func (m *BaseNotifier) NewIssue(ctx context.Context, issue *db.Issue) {}

func (m *BaseNotifier) NewFollow(ctx context.Context, follow *db.Follow) {}
func (m *BaseNotifier) DeleteFollow(ctx context.Context, follow *db.Follow) {}

func (m *BaseNotifier) NewPull(ctx context.Context, pull *db.Pull) {}
func (m *BaseNotifier) NewPullComment(ctx context.Context, comment *db.PullComment) {}

func (m *BaseNotifier) UpdateProfile(ctx context.Context, profile *db.Profile) {}
