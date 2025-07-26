package notify

import (
	"context"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/models"
)

type Notifier interface {
	NewRepo(ctx context.Context, repo *models.Repo)

	NewStar(ctx context.Context, star *models.Star)
	DeleteStar(ctx context.Context, star *models.Star)

	NewIssue(ctx context.Context, issue *models.Issue, mentions []syntax.DID)
	NewIssueComment(ctx context.Context, comment *models.IssueComment, mentions []syntax.DID)
	NewIssueState(ctx context.Context, actor syntax.DID, issue *models.Issue)
	DeleteIssue(ctx context.Context, issue *models.Issue)

	NewFollow(ctx context.Context, follow *models.Follow)
	DeleteFollow(ctx context.Context, follow *models.Follow)

	NewPull(ctx context.Context, pull *models.Pull)
	NewPullComment(ctx context.Context, comment *models.PullComment)
	NewPullState(ctx context.Context, actor syntax.DID, pull *models.Pull)

	UpdateProfile(ctx context.Context, profile *models.Profile)

	NewString(ctx context.Context, s *models.String)
	EditString(ctx context.Context, s *models.String)
	DeleteString(ctx context.Context, did, rkey string)
}

// BaseNotifier is a listener that does nothing
type BaseNotifier struct{}

var _ Notifier = &BaseNotifier{}

func (m *BaseNotifier) NewRepo(ctx context.Context, repo *models.Repo) {}

func (m *BaseNotifier) NewStar(ctx context.Context, star *models.Star)    {}
func (m *BaseNotifier) DeleteStar(ctx context.Context, star *models.Star) {}

func (m *BaseNotifier) NewIssue(ctx context.Context, issue *models.Issue, mentions []syntax.DID) {}
func (m *BaseNotifier) NewIssueComment(ctx context.Context, comment *models.IssueComment, mentions []syntax.DID) {
}
func (m *BaseNotifier) NewIssueState(ctx context.Context, actor syntax.DID, issue *models.Issue) {}
func (m *BaseNotifier) DeleteIssue(ctx context.Context, issue *models.Issue)                     {}

func (m *BaseNotifier) NewFollow(ctx context.Context, follow *models.Follow)    {}
func (m *BaseNotifier) DeleteFollow(ctx context.Context, follow *models.Follow) {}

func (m *BaseNotifier) NewPull(ctx context.Context, pull *models.Pull)                        {}
func (m *BaseNotifier) NewPullComment(ctx context.Context, models *models.PullComment)        {}
func (m *BaseNotifier) NewPullState(ctx context.Context, actor syntax.DID, pull *models.Pull) {}

func (m *BaseNotifier) UpdateProfile(ctx context.Context, profile *models.Profile) {}

func (m *BaseNotifier) NewString(ctx context.Context, s *models.String)    {}
func (m *BaseNotifier) EditString(ctx context.Context, s *models.String)   {}
func (m *BaseNotifier) DeleteString(ctx context.Context, did, rkey string) {}
