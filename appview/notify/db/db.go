package db

import (
	"context"
	"log"

	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/idresolver"
)

type databaseNotifier struct {
	db  *db.DB
	res *idresolver.Resolver
}

func NewDatabaseNotifier(database *db.DB, resolver *idresolver.Resolver) notify.Notifier {
	return &databaseNotifier{
		db:  database,
		res: resolver,
	}
}

var _ notify.Notifier = &databaseNotifier{}

func (n *databaseNotifier) NewRepo(ctx context.Context, repo *models.Repo) {
	// no-op for now
}

func (n *databaseNotifier) NewStar(ctx context.Context, star *models.Star) {
	var err error
	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", string(star.RepoAt)))
	if err != nil {
		log.Printf("NewStar: failed to get repos: %v", err)
		return
	}

	// don't notify yourself
	if repo.Did == star.StarredByDid {
		return
	}

	// check if user wants these notifications
	prefs, err := n.db.GetNotificationPreferences(ctx, repo.Did)
	if err != nil {
		log.Printf("NewStar: failed to get notification preferences for %s: %v", repo.Did, err)
		return
	}
	if !prefs.RepoStarred {
		return
	}

	notification := &models.Notification{
		RecipientDid: repo.Did,
		ActorDid:     star.StarredByDid,
		Type:         models.NotificationTypeRepoStarred,
		EntityType:   "repo",
		EntityId:     string(star.RepoAt),
		RepoId:       &repo.Id,
	}
	err = n.db.CreateNotification(ctx, notification)
	if err != nil {
		log.Printf("NewStar: failed to create notification: %v", err)
		return
	}
}

func (n *databaseNotifier) DeleteStar(ctx context.Context, star *models.Star) {
	// no-op
}

func (n *databaseNotifier) NewIssue(ctx context.Context, issue *models.Issue) {
	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", string(issue.RepoAt)))
	if err != nil {
		log.Printf("NewIssue: failed to get repos: %v", err)
		return
	}

	if repo.Did == issue.Did {
		return
	}

	prefs, err := n.db.GetNotificationPreferences(ctx, repo.Did)
	if err != nil {
		log.Printf("NewIssue: failed to get notification preferences for %s: %v", repo.Did, err)
		return
	}
	if !prefs.IssueCreated {
		return
	}

	notification := &models.Notification{
		RecipientDid: repo.Did,
		ActorDid:     issue.Did,
		Type:         models.NotificationTypeIssueCreated,
		EntityType:   "issue",
		EntityId:     string(issue.AtUri()),
		RepoId:       &repo.Id,
		IssueId:      &issue.Id,
	}

	err = n.db.CreateNotification(ctx, notification)
	if err != nil {
		log.Printf("NewIssue: failed to create notification: %v", err)
		return
	}
}

func (n *databaseNotifier) NewIssueComment(ctx context.Context, comment *models.IssueComment) {
	issues, err := db.GetIssues(n.db, db.FilterEq("at_uri", comment.IssueAt))
	if err != nil {
		log.Printf("NewIssueComment: failed to get issues: %v", err)
		return
	}
	if len(issues) == 0 {
		log.Printf("NewIssueComment: no issue found for %s", comment.IssueAt)
		return
	}
	issue := issues[0]

	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", string(issue.RepoAt)))
	if err != nil {
		log.Printf("NewIssueComment: failed to get repos: %v", err)
		return
	}

	recipients := make(map[string]bool)

	// notify issue author (if not the commenter)
	if issue.Did != comment.Did {
		prefs, err := n.db.GetNotificationPreferences(ctx, issue.Did)
		if err == nil && prefs.IssueCommented {
			recipients[issue.Did] = true
		} else if err != nil {
			log.Printf("NewIssueComment: failed to get preferences for issue author %s: %v", issue.Did, err)
		}
	}

	// notify repo owner (if not the commenter and not already added)
	if repo.Did != comment.Did && repo.Did != issue.Did {
		prefs, err := n.db.GetNotificationPreferences(ctx, repo.Did)
		if err == nil && prefs.IssueCommented {
			recipients[repo.Did] = true
		} else if err != nil {
			log.Printf("NewIssueComment: failed to get preferences for repo owner %s: %v", repo.Did, err)
		}
	}

	// create notifications for all recipients
	for recipientDid := range recipients {
		notification := &models.Notification{
			RecipientDid: recipientDid,
			ActorDid:     comment.Did,
			Type:         models.NotificationTypeIssueCommented,
			EntityType:   "issue",
			EntityId:     string(issue.AtUri()),
			RepoId:       &repo.Id,
			IssueId:      &issue.Id,
		}

		err = n.db.CreateNotification(ctx, notification)
		if err != nil {
			log.Printf("NewIssueComment: failed to create notification for %s: %v", recipientDid, err)
		}
	}
}

func (n *databaseNotifier) NewFollow(ctx context.Context, follow *models.Follow) {
	prefs, err := n.db.GetNotificationPreferences(ctx, follow.SubjectDid)
	if err != nil {
		log.Printf("NewFollow: failed to get notification preferences for %s: %v", follow.SubjectDid, err)
		return
	}
	if !prefs.Followed {
		return
	}

	notification := &models.Notification{
		RecipientDid: follow.SubjectDid,
		ActorDid:     follow.UserDid,
		Type:         models.NotificationTypeFollowed,
		EntityType:   "follow",
		EntityId:     follow.UserDid,
	}

	err = n.db.CreateNotification(ctx, notification)
	if err != nil {
		log.Printf("NewFollow: failed to create notification: %v", err)
		return
	}
}

func (n *databaseNotifier) DeleteFollow(ctx context.Context, follow *models.Follow) {
	// no-op
}

func (n *databaseNotifier) NewPull(ctx context.Context, pull *models.Pull) {
	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", string(pull.RepoAt)))
	if err != nil {
		log.Printf("NewPull: failed to get repos: %v", err)
		return
	}

	if repo.Did == pull.OwnerDid {
		return
	}

	prefs, err := n.db.GetNotificationPreferences(ctx, repo.Did)
	if err != nil {
		log.Printf("NewPull: failed to get notification preferences for %s: %v", repo.Did, err)
		return
	}
	if !prefs.PullCreated {
		return
	}

	notification := &models.Notification{
		RecipientDid: repo.Did,
		ActorDid:     pull.OwnerDid,
		Type:         models.NotificationTypePullCreated,
		EntityType:   "pull",
		EntityId:     string(pull.RepoAt),
		RepoId:       &repo.Id,
		PullId:       func() *int64 { id := int64(pull.ID); return &id }(),
	}

	err = n.db.CreateNotification(ctx, notification)
	if err != nil {
		log.Printf("NewPull: failed to create notification: %v", err)
		return
	}
}

func (n *databaseNotifier) NewPullComment(ctx context.Context, comment *models.PullComment) {
	pulls, err := db.GetPulls(n.db,
		db.FilterEq("repo_at", comment.RepoAt),
		db.FilterEq("pull_id", comment.PullId))
	if err != nil {
		log.Printf("NewPullComment: failed to get pulls: %v", err)
		return
	}
	if len(pulls) == 0 {
		log.Printf("NewPullComment: no pull found for %s PR %d", comment.RepoAt, comment.PullId)
		return
	}
	pull := pulls[0]

	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", comment.RepoAt))
	if err != nil {
		log.Printf("NewPullComment: failed to get repos: %v", err)
		return
	}

	recipients := make(map[string]bool)

	// notify pull request author (if not the commenter)
	if pull.OwnerDid != comment.OwnerDid {
		prefs, err := n.db.GetNotificationPreferences(ctx, pull.OwnerDid)
		if err == nil && prefs.PullCommented {
			recipients[pull.OwnerDid] = true
		} else if err != nil {
			log.Printf("NewPullComment: failed to get preferences for pull author %s: %v", pull.OwnerDid, err)
		}
	}

	// notify repo owner (if not the commenter and not already added)
	if repo.Did != comment.OwnerDid && repo.Did != pull.OwnerDid {
		prefs, err := n.db.GetNotificationPreferences(ctx, repo.Did)
		if err == nil && prefs.PullCommented {
			recipients[repo.Did] = true
		} else if err != nil {
			log.Printf("NewPullComment: failed to get preferences for repo owner %s: %v", repo.Did, err)
		}
	}

	for recipientDid := range recipients {
		notification := &models.Notification{
			RecipientDid: recipientDid,
			ActorDid:     comment.OwnerDid,
			Type:         models.NotificationTypePullCommented,
			EntityType:   "pull",
			EntityId:     comment.RepoAt,
			RepoId:       &repo.Id,
			PullId:       func() *int64 { id := int64(pull.ID); return &id }(),
		}

		err = n.db.CreateNotification(ctx, notification)
		if err != nil {
			log.Printf("NewPullComment: failed to create notification for %s: %v", recipientDid, err)
		}
	}
}

func (n *databaseNotifier) UpdateProfile(ctx context.Context, profile *models.Profile) {
	// no-op
}

func (n *databaseNotifier) DeleteString(ctx context.Context, did, rkey string) {
	// no-op
}

func (n *databaseNotifier) EditString(ctx context.Context, string *models.String) {
	// no-op
}

func (n *databaseNotifier) NewString(ctx context.Context, string *models.String) {
	// no-op
}

func (n *databaseNotifier) NewIssueClosed(ctx context.Context, issue *models.Issue) {
	// Get repo details
	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", string(issue.RepoAt)))
	if err != nil {
		log.Printf("NewIssueClosed: failed to get repos: %v", err)
		return
	}

	// Don't notify yourself
	if repo.Did == issue.Did {
		return
	}

	// Check if user wants these notifications
	prefs, err := n.db.GetNotificationPreferences(ctx, repo.Did)
	if err != nil {
		log.Printf("NewIssueClosed: failed to get notification preferences for %s: %v", repo.Did, err)
		return
	}
	if !prefs.IssueClosed {
		return
	}

	notification := &models.Notification{
		RecipientDid: repo.Did,
		ActorDid:     issue.Did,
		Type:         models.NotificationTypeIssueClosed,
		EntityType:   "issue",
		EntityId:     string(issue.AtUri()),
		RepoId:       &repo.Id,
		IssueId:      &issue.Id,
	}

	err = n.db.CreateNotification(ctx, notification)
	if err != nil {
		log.Printf("NewIssueClosed: failed to create notification: %v", err)
		return
	}
}

func (n *databaseNotifier) NewPullMerged(ctx context.Context, pull *models.Pull) {
	// Get repo details
	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", string(pull.RepoAt)))
	if err != nil {
		log.Printf("NewPullMerged: failed to get repos: %v", err)
		return
	}

	// Don't notify yourself
	if repo.Did == pull.OwnerDid {
		return
	}

	// Check if user wants these notifications
	prefs, err := n.db.GetNotificationPreferences(ctx, pull.OwnerDid)
	if err != nil {
		log.Printf("NewPullMerged: failed to get notification preferences for %s: %v", pull.OwnerDid, err)
		return
	}
	if !prefs.PullMerged {
		return
	}

	notification := &models.Notification{
		RecipientDid: pull.OwnerDid,
		ActorDid:     repo.Did,
		Type:         models.NotificationTypePullMerged,
		EntityType:   "pull",
		EntityId:     string(pull.RepoAt),
		RepoId:       &repo.Id,
		PullId:       func() *int64 { id := int64(pull.ID); return &id }(),
	}

	err = n.db.CreateNotification(ctx, notification)
	if err != nil {
		log.Printf("NewPullMerged: failed to create notification: %v", err)
		return
	}
}

func (n *databaseNotifier) NewPullClosed(ctx context.Context, pull *models.Pull) {
	// Get repo details
	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", string(pull.RepoAt)))
	if err != nil {
		log.Printf("NewPullClosed: failed to get repos: %v", err)
		return
	}

	// Don't notify yourself
	if repo.Did == pull.OwnerDid {
		return
	}

	// Check if user wants these notifications - reuse pull_merged preference for now
	prefs, err := n.db.GetNotificationPreferences(ctx, pull.OwnerDid)
	if err != nil {
		log.Printf("NewPullClosed: failed to get notification preferences for %s: %v", pull.OwnerDid, err)
		return
	}
	if !prefs.PullMerged {
		return
	}

	notification := &models.Notification{
		RecipientDid: pull.OwnerDid,
		ActorDid:     repo.Did,
		Type:         models.NotificationTypePullClosed,
		EntityType:   "pull",
		EntityId:     string(pull.RepoAt),
		RepoId:       &repo.Id,
		PullId:       func() *int64 { id := int64(pull.ID); return &id }(),
	}

	err = n.db.CreateNotification(ctx, notification)
	if err != nil {
		log.Printf("NewPullClosed: failed to create notification: %v", err)
		return
	}
}
