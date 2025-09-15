package models

import "time"

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

type NotificationWithEntity struct {
	*Notification
	Repo  *Repo
	Issue *Issue
	Pull  *Pull
}

type NotificationPreferences struct {
	ID                 int64
	UserDid            string
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
