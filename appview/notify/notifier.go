package notify

import (
	"context"

	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
)

type Notifier interface {
	NewRepo(ctx context.Context, repo *models.Repo)

	NewStar(ctx context.Context, star *models.Star)
	DeleteStar(ctx context.Context, star *models.Star)

	NewIssue(ctx context.Context, issue *models.Issue)

	NewFollow(ctx context.Context, follow *models.Follow)
	DeleteFollow(ctx context.Context, follow *models.Follow)

	NewPull(ctx context.Context, pull *models.Pull)
	NewPullComment(ctx context.Context, comment *models.PullComment)

	UpdateProfile(ctx context.Context, profile *models.Profile)

	NewString(ctx context.Context, s *db.String)
	EditString(ctx context.Context, s *db.String)
	DeleteString(ctx context.Context, did, rkey string)
}

// BaseNotifier is a listener that does nothing
type BaseNotifier struct{}

var _ Notifier = &BaseNotifier{}

func (m *BaseNotifier) NewRepo(ctx context.Context, repo *models.Repo) {}

func (m *BaseNotifier) NewStar(ctx context.Context, star *models.Star)    {}
func (m *BaseNotifier) DeleteStar(ctx context.Context, star *models.Star) {}

func (m *BaseNotifier) NewIssue(ctx context.Context, issue *models.Issue) {}

func (m *BaseNotifier) NewFollow(ctx context.Context, follow *models.Follow)    {}
func (m *BaseNotifier) DeleteFollow(ctx context.Context, follow *models.Follow) {}

func (m *BaseNotifier) NewPull(ctx context.Context, pull *models.Pull)                 {}
func (m *BaseNotifier) NewPullComment(ctx context.Context, models *models.PullComment) {}

func (m *BaseNotifier) UpdateProfile(ctx context.Context, profile *models.Profile) {}

func (m *BaseNotifier) NewString(ctx context.Context, s *db.String)        {}
func (m *BaseNotifier) EditString(ctx context.Context, s *db.String)       {}
func (m *BaseNotifier) DeleteString(ctx context.Context, did, rkey string) {}
