package notify

import (
	"context"
	"log/slog"
	"reflect"
	"sync"

	"tangled.org/core/appview/models"
	"tangled.org/core/log"
)

type mergedNotifier struct {
	notifiers []Notifier
	logger    *slog.Logger
}

func NewMergedNotifier(notifiers []Notifier, logger *slog.Logger) Notifier {
	return &mergedNotifier{notifiers, logger}
}

var _ Notifier = &mergedNotifier{}

// fanout calls the same method on all notifiers concurrently
func (m *mergedNotifier) fanout(method string, ctx context.Context, args ...any) {
	ctx = log.IntoContext(ctx, m.logger.With("method", method))
	var wg sync.WaitGroup
	for _, n := range m.notifiers {
		wg.Add(1)
		go func(notifier Notifier) {
			defer wg.Done()
			v := reflect.ValueOf(notifier).MethodByName(method)
			in := make([]reflect.Value, len(args)+1)
			in[0] = reflect.ValueOf(ctx)
			for i, arg := range args {
				in[i+1] = reflect.ValueOf(arg)
			}
			v.Call(in)
		}(n)
	}
	wg.Wait()
}

func (m *mergedNotifier) NewRepo(ctx context.Context, repo *models.Repo) {
	m.fanout("NewRepo", ctx, repo)
}

func (m *mergedNotifier) NewStar(ctx context.Context, star *models.Star) {
	m.fanout("NewStar", ctx, star)
}

func (m *mergedNotifier) DeleteStar(ctx context.Context, star *models.Star) {
	m.fanout("DeleteStar", ctx, star)
}

func (m *mergedNotifier) NewIssue(ctx context.Context, issue *models.Issue) {
	m.fanout("NewIssue", ctx, issue)
}

func (m *mergedNotifier) NewIssueComment(ctx context.Context, comment *models.IssueComment) {
	m.fanout("NewIssueComment", ctx, comment)
}

func (m *mergedNotifier) NewIssueClosed(ctx context.Context, issue *models.Issue) {
	m.fanout("NewIssueClosed", ctx, issue)
}

func (m *mergedNotifier) DeleteIssue(ctx context.Context, issue *models.Issue) {
	m.fanout("DeleteIssue", ctx, issue)
}

func (m *mergedNotifier) NewFollow(ctx context.Context, follow *models.Follow) {
	m.fanout("NewFollow", ctx, follow)
}

func (m *mergedNotifier) DeleteFollow(ctx context.Context, follow *models.Follow) {
	m.fanout("DeleteFollow", ctx, follow)
}

func (m *mergedNotifier) NewPull(ctx context.Context, pull *models.Pull) {
	m.fanout("NewPull", ctx, pull)
}

func (m *mergedNotifier) NewPullComment(ctx context.Context, comment *models.PullComment) {
	m.fanout("NewPullComment", ctx, comment)
}

func (m *mergedNotifier) NewPullMerged(ctx context.Context, pull *models.Pull) {
	m.fanout("NewPullMerged", ctx, pull)
}

func (m *mergedNotifier) NewPullClosed(ctx context.Context, pull *models.Pull) {
	m.fanout("NewPullClosed", ctx, pull)
}

func (m *mergedNotifier) UpdateProfile(ctx context.Context, profile *models.Profile) {
	m.fanout("UpdateProfile", ctx, profile)
}

func (m *mergedNotifier) NewString(ctx context.Context, s *models.String) {
	m.fanout("NewString", ctx, s)
}

func (m *mergedNotifier) EditString(ctx context.Context, s *models.String) {
	m.fanout("EditString", ctx, s)
}

func (m *mergedNotifier) DeleteString(ctx context.Context, did, rkey string) {
	m.fanout("DeleteString", ctx, did, rkey)
}
