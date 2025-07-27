package posthog_service

import (
	"context"
	"log"

	"github.com/posthog/posthog-go"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/notify"
)

type posthogNotifier struct {
	client posthog.Client
	notify.BaseNotifier
}

func NewPosthogNotifier(client posthog.Client) notify.Notifier {
	return &posthogNotifier{
		client,
		notify.BaseNotifier{},
	}
}

var _ notify.Notifier = &posthogNotifier{}

func (n *posthogNotifier) NewRepo(ctx context.Context, repo *db.Repo) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: repo.Did,
		Event:      "new_repo",
		Properties: posthog.Properties{"repo": repo.Name, "repo_at": repo.RepoAt()},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewStar(ctx context.Context, star *db.Star) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: star.StarredByDid,
		Event:      "star",
		Properties: posthog.Properties{"repo_at": star.RepoAt.String()},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) DeleteStar(ctx context.Context, star *db.Star) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: star.StarredByDid,
		Event:      "unstar",
		Properties: posthog.Properties{"repo_at": star.RepoAt.String()},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewIssue(ctx context.Context, issue *db.Issue) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: issue.OwnerDid,
		Event:      "new_issue",
		Properties: posthog.Properties{
			"repo_at":  issue.RepoAt.String(),
			"issue_id": issue.IssueId,
		},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewPull(ctx context.Context, pull *db.Pull) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: pull.OwnerDid,
		Event:      "new_pull",
		Properties: posthog.Properties{
			"repo_at": pull.RepoAt,
			"pull_id": pull.PullId,
		},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewPullComment(ctx context.Context, comment *db.PullComment) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: comment.OwnerDid,
		Event:      "new_pull_comment",
		Properties: posthog.Properties{
			"repo_at": comment.RepoAt,
			"pull_id": comment.PullId,
		},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewFollow(ctx context.Context, follow *db.Follow) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: follow.UserDid,
		Event:      "follow",
		Properties: posthog.Properties{"subject": follow.SubjectDid},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) DeleteFollow(ctx context.Context, follow *db.Follow) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: follow.UserDid,
		Event:      "unfollow",
		Properties: posthog.Properties{"subject": follow.SubjectDid},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) UpdateProfile(ctx context.Context, profile *db.Profile) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: profile.Did,
		Event:      "edit_profile",
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}
