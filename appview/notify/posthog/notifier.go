package posthog

import (
	"context"
	"log"

	"github.com/posthog/posthog-go"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
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

func (n *posthogNotifier) NewRepo(ctx context.Context, repo *models.Repo) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: repo.Did,
		Event:      "new_repo",
		Properties: posthog.Properties{"repo": repo.Name, "repo_at": repo.RepoAt()},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewStar(ctx context.Context, star *models.Star) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: star.StarredByDid,
		Event:      "star",
		Properties: posthog.Properties{"repo_at": star.RepoAt.String()},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) DeleteStar(ctx context.Context, star *models.Star) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: star.StarredByDid,
		Event:      "unstar",
		Properties: posthog.Properties{"repo_at": star.RepoAt.String()},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewIssue(ctx context.Context, issue *models.Issue) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: issue.Did,
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

func (n *posthogNotifier) NewPull(ctx context.Context, pull *models.Pull) {
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

func (n *posthogNotifier) NewPullComment(ctx context.Context, comment *models.PullComment) {
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

func (n *posthogNotifier) NewPullClosed(ctx context.Context, pull *models.Pull) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: pull.OwnerDid,
		Event:      "pull_closed",
		Properties: posthog.Properties{
			"repo_at": pull.RepoAt,
			"pull_id": pull.PullId,
		},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewFollow(ctx context.Context, follow *models.Follow) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: follow.UserDid,
		Event:      "follow",
		Properties: posthog.Properties{"subject": follow.SubjectDid},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) DeleteFollow(ctx context.Context, follow *models.Follow) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: follow.UserDid,
		Event:      "unfollow",
		Properties: posthog.Properties{"subject": follow.SubjectDid},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) UpdateProfile(ctx context.Context, profile *models.Profile) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: profile.Did,
		Event:      "edit_profile",
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) DeleteString(ctx context.Context, did, rkey string) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: did,
		Event:      "delete_string",
		Properties: posthog.Properties{"rkey": rkey},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) EditString(ctx context.Context, string *models.String) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: string.Did.String(),
		Event:      "edit_string",
		Properties: posthog.Properties{"rkey": string.Rkey},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewString(ctx context.Context, string *models.String) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: string.Did.String(),
		Event:      "new_string",
		Properties: posthog.Properties{"rkey": string.Rkey},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewIssueComment(ctx context.Context, comment *models.IssueComment) {
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: comment.Did,
		Event:      "new_issue_comment",
		Properties: posthog.Properties{
			"issue_at": comment.IssueAt,
		},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewIssueState(ctx context.Context, issue *models.Issue) {
	var event string
	if issue.Open {
		event = "issue_reopen"
	} else {
		event = "issue_closed"
	}
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: issue.Did,
		Event:      event,
		Properties: posthog.Properties{
			"repo_at":  issue.RepoAt.String(),
			"issue_id": issue.IssueId,
		},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}

func (n *posthogNotifier) NewPullState(ctx context.Context, pull *models.Pull) {
	var event string
	switch pull.State {
	case models.PullClosed:
		event = "pull_closed"
	case models.PullOpen:
		event = "pull_reopen"
	case models.PullMerged:
		event = "pull_merged"
	default:
		log.Println("posthog: unexpected new PR state:", pull.State)
		return
	}
	err := n.client.Enqueue(posthog.Capture{
		DistinctId: pull.OwnerDid,
		Event:      event,
		Properties: posthog.Properties{
			"repo_at": pull.RepoAt,
			"pull_id": pull.PullId,
		},
	})
	if err != nil {
		log.Println("failed to enqueue posthog event:", err)
	}
}
