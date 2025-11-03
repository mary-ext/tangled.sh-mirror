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
	NotificationTypeIssueReopen    NotificationType = "issue_reopen"
	NotificationTypePullClosed     NotificationType = "pull_closed"
	NotificationTypePullReopen     NotificationType = "pull_reopen"
	NotificationTypeUserMentioned  NotificationType = "user_mentioned"
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
	case NotificationTypeIssueReopen:
		return "circle-dot"
	case NotificationTypePullCreated:
		return "git-pull-request-create"
	case NotificationTypePullCommented:
		return "message-square"
	case NotificationTypePullMerged:
		return "git-merge"
	case NotificationTypePullClosed:
		return "git-pull-request-closed"
	case NotificationTypePullReopen:
		return "git-pull-request-create"
	case NotificationTypeFollowed:
		return "user-plus"
	case NotificationTypeUserMentioned:
		return "at-sign"
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
	UserMentioned      bool
	PullMerged         bool
	IssueClosed        bool
	EmailNotifications bool
}

func (prefs *NotificationPreferences) ShouldNotify(t NotificationType) bool {
	switch t {
	case NotificationTypeRepoStarred:
		return prefs.RepoStarred
	case NotificationTypeIssueCreated:
		return prefs.IssueCreated
	case NotificationTypeIssueCommented:
		return prefs.IssueCommented
	case NotificationTypeIssueClosed:
		return prefs.IssueClosed
	case NotificationTypeIssueReopen:
		return prefs.IssueCreated // smae pref for now
	case NotificationTypePullCreated:
		return prefs.PullCreated
	case NotificationTypePullCommented:
		return prefs.PullCommented
	case NotificationTypePullMerged:
		return prefs.PullMerged
	case NotificationTypePullClosed:
		return prefs.PullMerged // same pref for now
	case NotificationTypePullReopen:
		return prefs.PullCreated // same pref for now
	case NotificationTypeFollowed:
		return prefs.Followed
	case NotificationTypeUserMentioned:
		return prefs.UserMentioned
	default:
		return false
	}
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
		UserMentioned:      true,
		PullMerged:         true,
		IssueClosed:        true,
		EmailNotifications: false,
	}
}
