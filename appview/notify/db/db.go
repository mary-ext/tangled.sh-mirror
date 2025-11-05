package db

import (
	"context"
	"log"
	"maps"
	"slices"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/idresolver"
)

const (
	maxMentions = 5
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

	actorDid := syntax.DID(star.StarredByDid)
	recipients := []syntax.DID{syntax.DID(repo.Did)}
	eventType := models.NotificationTypeRepoStarred
	entityType := "repo"
	entityId := star.RepoAt.String()
	repoId := &repo.Id
	var issueId *int64
	var pullId *int64

	n.notifyEvent(
		actorDid,
		recipients,
		eventType,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
}

func (n *databaseNotifier) DeleteStar(ctx context.Context, star *models.Star) {
	// no-op
}

func (n *databaseNotifier) NewIssue(ctx context.Context, issue *models.Issue, mentions []syntax.DID) {

	// build the recipients list
	// - owner of the repo
	// - collaborators in the repo
	var recipients []syntax.DID
	recipients = append(recipients, syntax.DID(issue.Repo.Did))
	collaborators, err := db.GetCollaborators(n.db, db.FilterEq("repo_at", issue.Repo.RepoAt()))
	if err != nil {
		log.Printf("failed to fetch collaborators: %v", err)
		return
	}
	for _, c := range collaborators {
		recipients = append(recipients, c.SubjectDid)
	}

	actorDid := syntax.DID(issue.Did)
	entityType := "issue"
	entityId := issue.AtUri().String()
	repoId := &issue.Repo.Id
	issueId := &issue.Id
	var pullId *int64

	n.notifyEvent(
		actorDid,
		recipients,
		models.NotificationTypeIssueCreated,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
	n.notifyEvent(
		actorDid,
		mentions,
		models.NotificationTypeUserMentioned,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
}

func (n *databaseNotifier) NewIssueComment(ctx context.Context, comment *models.IssueComment, mentions []syntax.DID) {
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

	var recipients []syntax.DID
	recipients = append(recipients, syntax.DID(issue.Repo.Did))

	if comment.IsReply() {
		// if this comment is a reply, then notify everybody in that thread
		parentAtUri := *comment.ReplyTo
		allThreads := issue.CommentList()

		// find the parent thread, and add all DIDs from here to the recipient list
		for _, t := range allThreads {
			if t.Self.AtUri().String() == parentAtUri {
				recipients = append(recipients, t.Participants()...)
			}
		}
	} else {
		// not a reply, notify just the issue author
		recipients = append(recipients, syntax.DID(issue.Did))
	}

	actorDid := syntax.DID(comment.Did)
	entityType := "issue"
	entityId := issue.AtUri().String()
	repoId := &issue.Repo.Id
	issueId := &issue.Id
	var pullId *int64

	n.notifyEvent(
		actorDid,
		recipients,
		models.NotificationTypeIssueCommented,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
	n.notifyEvent(
		actorDid,
		mentions,
		models.NotificationTypeUserMentioned,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
}

func (n *databaseNotifier) DeleteIssue(ctx context.Context, issue *models.Issue) {
	// no-op for now
}

func (n *databaseNotifier) NewFollow(ctx context.Context, follow *models.Follow) {
	actorDid := syntax.DID(follow.UserDid)
	recipients := []syntax.DID{syntax.DID(follow.SubjectDid)}
	eventType := models.NotificationTypeFollowed
	entityType := "follow"
	entityId := follow.UserDid
	var repoId, issueId, pullId *int64

	n.notifyEvent(
		actorDid,
		recipients,
		eventType,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
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

	// build the recipients list
	// - owner of the repo
	// - collaborators in the repo
	var recipients []syntax.DID
	recipients = append(recipients, syntax.DID(repo.Did))
	collaborators, err := db.GetCollaborators(n.db, db.FilterEq("repo_at", repo.RepoAt()))
	if err != nil {
		log.Printf("failed to fetch collaborators: %v", err)
		return
	}
	for _, c := range collaborators {
		recipients = append(recipients, c.SubjectDid)
	}

	actorDid := syntax.DID(pull.OwnerDid)
	eventType := models.NotificationTypePullCreated
	entityType := "pull"
	entityId := pull.AtUri().String()
	repoId := &repo.Id
	var issueId *int64
	p := int64(pull.ID)
	pullId := &p

	n.notifyEvent(
		actorDid,
		recipients,
		eventType,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
}

func (n *databaseNotifier) NewPullComment(ctx context.Context, comment *models.PullComment, mentions []syntax.DID) {
	pull, err := db.GetPull(n.db,
		syntax.ATURI(comment.RepoAt),
		comment.PullId,
	)
	if err != nil {
		log.Printf("NewPullComment: failed to get pulls: %v", err)
		return
	}

	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", comment.RepoAt))
	if err != nil {
		log.Printf("NewPullComment: failed to get repos: %v", err)
		return
	}

	// build up the recipients list:
	// - repo owner
	// - all pull participants
	var recipients []syntax.DID
	recipients = append(recipients, syntax.DID(repo.Did))
	for _, p := range pull.Participants() {
		recipients = append(recipients, syntax.DID(p))
	}

	actorDid := syntax.DID(comment.OwnerDid)
	eventType := models.NotificationTypePullCommented
	entityType := "pull"
	entityId := pull.AtUri().String()
	repoId := &repo.Id
	var issueId *int64
	p := int64(pull.ID)
	pullId := &p

	n.notifyEvent(
		actorDid,
		recipients,
		eventType,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
	n.notifyEvent(
		actorDid,
		mentions,
		models.NotificationTypeUserMentioned,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
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

func (n *databaseNotifier) NewIssueState(ctx context.Context, actor syntax.DID, issue *models.Issue) {
	// build up the recipients list:
	// - repo owner
	// - repo collaborators
	// - all issue participants
	var recipients []syntax.DID
	recipients = append(recipients, syntax.DID(issue.Repo.Did))
	collaborators, err := db.GetCollaborators(n.db, db.FilterEq("repo_at", issue.Repo.RepoAt()))
	if err != nil {
		log.Printf("failed to fetch collaborators: %v", err)
		return
	}
	for _, c := range collaborators {
		recipients = append(recipients, c.SubjectDid)
	}
	for _, p := range issue.Participants() {
		recipients = append(recipients, syntax.DID(p))
	}

	entityType := "pull"
	entityId := issue.AtUri().String()
	repoId := &issue.Repo.Id
	issueId := &issue.Id
	var pullId *int64
	var eventType models.NotificationType

	if issue.Open {
		eventType = models.NotificationTypeIssueReopen
	} else {
		eventType = models.NotificationTypeIssueClosed
	}

	n.notifyEvent(
		actor,
		recipients,
		eventType,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
}

func (n *databaseNotifier) NewPullState(ctx context.Context, actor syntax.DID, pull *models.Pull) {
	// Get repo details
	repo, err := db.GetRepo(n.db, db.FilterEq("at_uri", string(pull.RepoAt)))
	if err != nil {
		log.Printf("NewPullState: failed to get repos: %v", err)
		return
	}

	// build up the recipients list:
	// - repo owner
	// - all pull participants
	var recipients []syntax.DID
	recipients = append(recipients, syntax.DID(repo.Did))
	collaborators, err := db.GetCollaborators(n.db, db.FilterEq("repo_at", repo.RepoAt()))
	if err != nil {
		log.Printf("failed to fetch collaborators: %v", err)
		return
	}
	for _, c := range collaborators {
		recipients = append(recipients, c.SubjectDid)
	}
	for _, p := range pull.Participants() {
		recipients = append(recipients, syntax.DID(p))
	}

	entityType := "pull"
	entityId := pull.AtUri().String()
	repoId := &repo.Id
	var issueId *int64
	var eventType models.NotificationType
	switch pull.State {
	case models.PullClosed:
		eventType = models.NotificationTypePullClosed
	case models.PullOpen:
		eventType = models.NotificationTypePullReopen
	case models.PullMerged:
		eventType = models.NotificationTypePullMerged
	default:
		log.Println("NewPullState: unexpected new PR state:", pull.State)
		return
	}
	p := int64(pull.ID)
	pullId := &p

	n.notifyEvent(
		actor,
		recipients,
		eventType,
		entityType,
		entityId,
		repoId,
		issueId,
		pullId,
	)
}

func (n *databaseNotifier) notifyEvent(
	actorDid syntax.DID,
	recipients []syntax.DID,
	eventType models.NotificationType,
	entityType string,
	entityId string,
	repoId *int64,
	issueId *int64,
	pullId *int64,
) {
	if eventType == models.NotificationTypeUserMentioned && len(recipients) > maxMentions {
		recipients = recipients[:maxMentions]
	}
	recipientSet := make(map[syntax.DID]struct{})
	for _, did := range recipients {
		// everybody except actor themselves
		if did != actorDid {
			recipientSet[did] = struct{}{}
		}
	}

	prefMap, err := db.GetNotificationPreferences(
		n.db,
		db.FilterIn("user_did", slices.Collect(maps.Keys(recipientSet))),
	)
	if err != nil {
		// failed to get prefs for users
		return
	}

	// create a transaction for bulk notification storage
	tx, err := n.db.Begin()
	if err != nil {
		// failed to start tx
		return
	}
	defer tx.Rollback()

	// filter based on preferences
	for recipientDid := range recipientSet {
		prefs, ok := prefMap[recipientDid]
		if !ok {
			prefs = models.DefaultNotificationPreferences(recipientDid)
		}

		// skip users who don’t want this type
		if !prefs.ShouldNotify(eventType) {
			continue
		}

		// create notification
		notif := &models.Notification{
			RecipientDid: recipientDid.String(),
			ActorDid:     actorDid.String(),
			Type:         eventType,
			EntityType:   entityType,
			EntityId:     entityId,
			RepoId:       repoId,
			IssueId:      issueId,
			PullId:       pullId,
		}

		if err := db.CreateNotification(tx, notif); err != nil {
			log.Printf("notifyEvent: failed to create notification for %s: %v", recipientDid, err)
		}
	}

	if err := tx.Commit(); err != nil {
		// failed to commit
		return
	}
}
