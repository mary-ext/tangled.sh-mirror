package models

import (
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type NotificationType string

const (
	NotificationTypeRepoStarred    NotificationType = "repo_starred"
	NotificationTypeIssueCreated   NotificationType = "issue_created"
	NotificationTypeIssueCommented NotificationType = "issue_commented"
	NotificationTypePullCreated    NotificationType = "pull_created"
	NotificationTypePullCommented  NotificationType = "pull_commented"
	NotificationTypeFollowed       NotificationType = "followed"
	NotificationTypePullMerged     NotificationType = "pull_merged"
	NotificationTypeIssueClosed    NotificationType = "issue_closed"
	NotificationTypePullClosed     NotificationType = "pull_closed"
)

type Notification struct {
	ID           int64
	RecipientDid string
	ActorDid     string
	Type         NotificationType
	EntityType   string
	EntityId     string
	Read         bool
	Created      time.Time

	// foreign key references
	RepoId  *int64
	IssueId *int64
	PullId  *int64
}

// lucide icon that represents this notification
func (n *Notification) Icon() string {
	switch n.Type {
	case NotificationTypeRepoStarred:
		return "star"
	case NotificationTypeIssueCreated:
		return "circle-dot"
	case NotificationTypeIssueCommented:
		return "message-square"
	case NotificationTypeIssueClosed:
		return "ban"
	case NotificationTypePullCreated:
		return "git-pull-request-create"
	case NotificationTypePullCommented:
		return "message-square"
	case NotificationTypePullMerged:
		return "git-merge"
	case NotificationTypePullClosed:
		return "git-pull-request-closed"
	case NotificationTypeFollowed:
		return "user-plus"
	default:
		return ""
	}
}

type NotificationWithEntity struct {
	*Notification
	Repo  *Repo
	Issue *Issue
	Pull  *Pull
}

type NotificationPreferences struct {
	ID                 int64
	UserDid            syntax.DID
	RepoStarred        bool
	IssueCreated       bool
	IssueCommented     bool
	PullCreated        bool
	PullCommented      bool
	Followed           bool
	PullMerged         bool
	IssueClosed        bool
	EmailNotifications bool
}
func DefaultNotificationPreferences(user syntax.DID) *NotificationPreferences {
	return &NotificationPreferences{
		UserDid:            user,
		RepoStarred:        true,
		IssueCreated:       true,
		IssueCommented:     true,
		PullCreated:        true,
		PullCommented:      true,
		Followed:           true,
		PullMerged:         true,
		IssueClosed:        true,
		EmailNotifications: false,
	}
}
