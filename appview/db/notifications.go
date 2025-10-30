package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pagination"
)

func CreateNotification(e Execer, notification *models.Notification) error {
	query := `
		INSERT INTO notifications (recipient_did, actor_did, type, entity_type, entity_id, read, repo_id, issue_id, pull_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := e.Exec(query,
		notification.RecipientDid,
		notification.ActorDid,
		string(notification.Type),
		notification.EntityType,
		notification.EntityId,
		notification.Read,
		notification.RepoId,
		notification.IssueId,
		notification.PullId,
	)
	if err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get notification ID: %w", err)
	}

	notification.ID = id
	return nil
}

// GetNotificationsPaginated retrieves notifications with filters and pagination
func GetNotificationsPaginated(e Execer, page pagination.Page, filters ...filter) ([]*models.Notification, error) {
	var conditions []string
	var args []any

	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + conditions[0]
		for _, condition := range conditions[1:] {
			whereClause += " AND " + condition
		}
	}
	pageClause := ""
	if page.Limit > 0 {
		pageClause = " limit ? offset ? "
		args = append(args, page.Limit, page.Offset)
	}

	query := fmt.Sprintf(`
		select id, recipient_did, actor_did, type, entity_type, entity_id, read, created, repo_id, issue_id, pull_id
		from notifications
		%s
		order by created desc
		%s
	`, whereClause, pageClause)

	rows, err := e.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query notifications: %w", err)
	}
	defer rows.Close()

	var notifications []*models.Notification
	for rows.Next() {
		var n models.Notification
		var typeStr string
		var createdStr string
		err := rows.Scan(
			&n.ID,
			&n.RecipientDid,
			&n.ActorDid,
			&typeStr,
			&n.EntityType,
			&n.EntityId,
			&n.Read,
			&createdStr,
			&n.RepoId,
			&n.IssueId,
			&n.PullId,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan notification: %w", err)
		}
		n.Type = models.NotificationType(typeStr)
		n.Created, err = time.Parse(time.RFC3339, createdStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse created timestamp: %w", err)
		}
		notifications = append(notifications, &n)
	}

	return notifications, nil
}

// GetNotificationsWithEntities retrieves notifications with their related entities
func GetNotificationsWithEntities(e Execer, page pagination.Page, filters ...filter) ([]*models.NotificationWithEntity, error) {
	var conditions []string
	var args []any

	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + conditions[0]
		for _, condition := range conditions[1:] {
			whereClause += " AND " + condition
		}
	}

	query := fmt.Sprintf(`
		select
			n.id, n.recipient_did, n.actor_did, n.type, n.entity_type, n.entity_id,
			n.read, n.created, n.repo_id, n.issue_id, n.pull_id,
			r.id as r_id, r.did as r_did, r.name as r_name, r.description as r_description, r.website as r_website, r.topics as r_topics,
			i.id as i_id, i.did as i_did, i.issue_id as i_issue_id, i.title as i_title, i.open as i_open,
			p.id as p_id, p.owner_did as p_owner_did, p.pull_id as p_pull_id, p.title as p_title, p.state as p_state
		from notifications n
		left join repos r on n.repo_id = r.id
		left join issues i on n.issue_id = i.id
		left join pulls p on n.pull_id = p.id
		%s
		order by n.created desc
		limit ? offset ?
	`, whereClause)

	args = append(args, page.Limit, page.Offset)

	rows, err := e.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query notifications with entities: %w", err)
	}
	defer rows.Close()

	var notifications []*models.NotificationWithEntity
	for rows.Next() {
		var n models.Notification
		var typeStr string
		var createdStr string
		var repo models.Repo
		var issue models.Issue
		var pull models.Pull
		var rId, iId, pId sql.NullInt64
		var rDid, rName, rDescription, rWebsite, rTopicStr sql.NullString
		var iDid sql.NullString
		var iIssueId sql.NullInt64
		var iTitle sql.NullString
		var iOpen sql.NullBool
		var pOwnerDid sql.NullString
		var pPullId sql.NullInt64
		var pTitle sql.NullString
		var pState sql.NullInt64

		err := rows.Scan(
			&n.ID, &n.RecipientDid, &n.ActorDid, &typeStr, &n.EntityType, &n.EntityId,
			&n.Read, &createdStr, &n.RepoId, &n.IssueId, &n.PullId,
			&rId, &rDid, &rName, &rDescription, &rWebsite, &rTopicStr,
			&iId, &iDid, &iIssueId, &iTitle, &iOpen,
			&pId, &pOwnerDid, &pPullId, &pTitle, &pState,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan notification with entities: %w", err)
		}

		n.Type = models.NotificationType(typeStr)
		n.Created, err = time.Parse(time.RFC3339, createdStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse created timestamp: %w", err)
		}

		nwe := &models.NotificationWithEntity{Notification: &n}

		// populate repo if present
		if rId.Valid {
			repo.Id = rId.Int64
			if rDid.Valid {
				repo.Did = rDid.String
			}
			if rName.Valid {
				repo.Name = rName.String
			}
			if rDescription.Valid {
				repo.Description = rDescription.String
			}
			if rWebsite.Valid {
				repo.Website = rWebsite.String
			}
			if rTopicStr.Valid {
				repo.Topics = strings.Fields(rTopicStr.String)
			}
			nwe.Repo = &repo
		}

		// populate issue if present
		if iId.Valid {
			issue.Id = iId.Int64
			if iDid.Valid {
				issue.Did = iDid.String
			}
			if iIssueId.Valid {
				issue.IssueId = int(iIssueId.Int64)
			}
			if iTitle.Valid {
				issue.Title = iTitle.String
			}
			if iOpen.Valid {
				issue.Open = iOpen.Bool
			}
			nwe.Issue = &issue
		}

		// populate pull if present
		if pId.Valid {
			pull.ID = int(pId.Int64)
			if pOwnerDid.Valid {
				pull.OwnerDid = pOwnerDid.String
			}
			if pPullId.Valid {
				pull.PullId = int(pPullId.Int64)
			}
			if pTitle.Valid {
				pull.Title = pTitle.String
			}
			if pState.Valid {
				pull.State = models.PullState(pState.Int64)
			}
			nwe.Pull = &pull
		}

		notifications = append(notifications, nwe)
	}

	return notifications, nil
}

// GetNotifications retrieves notifications with filters
func GetNotifications(e Execer, filters ...filter) ([]*models.Notification, error) {
	return GetNotificationsPaginated(e, pagination.FirstPage(), filters...)
}

func CountNotifications(e Execer, filters ...filter) (int64, error) {
	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(`select count(1) from notifications %s`, whereClause)
	var count int64
	err := e.QueryRow(query, args...).Scan(&count)

	if !errors.Is(err, sql.ErrNoRows) && err != nil {
		return 0, err
	}

	return count, nil
}

func MarkNotificationRead(e Execer, notificationID int64, userDID string) error {
	idFilter := FilterEq("id", notificationID)
	recipientFilter := FilterEq("recipient_did", userDID)

	query := fmt.Sprintf(`
		UPDATE notifications
		SET read = 1
		WHERE %s AND %s
	`, idFilter.Condition(), recipientFilter.Condition())

	args := append(idFilter.Arg(), recipientFilter.Arg()...)

	result, err := e.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to mark notification as read: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("notification not found or access denied")
	}

	return nil
}

func MarkAllNotificationsRead(e Execer, userDID string) error {
	recipientFilter := FilterEq("recipient_did", userDID)
	readFilter := FilterEq("read", 0)

	query := fmt.Sprintf(`
		UPDATE notifications
		SET read = 1
		WHERE %s AND %s
	`, recipientFilter.Condition(), readFilter.Condition())

	args := append(recipientFilter.Arg(), readFilter.Arg()...)

	_, err := e.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to mark all notifications as read: %w", err)
	}

	return nil
}

func DeleteNotification(e Execer, notificationID int64, userDID string) error {
	idFilter := FilterEq("id", notificationID)
	recipientFilter := FilterEq("recipient_did", userDID)

	query := fmt.Sprintf(`
		DELETE FROM notifications
		WHERE %s AND %s
	`, idFilter.Condition(), recipientFilter.Condition())

	args := append(idFilter.Arg(), recipientFilter.Arg()...)

	result, err := e.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete notification: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("notification not found or access denied")
	}

	return nil
}

func GetNotificationPreference(e Execer, userDid string) (*models.NotificationPreferences, error) {
	prefs, err := GetNotificationPreferences(e, FilterEq("user_did", userDid))
	if err != nil {
		return nil, err
	}

	p, ok := prefs[syntax.DID(userDid)]
	if !ok {
		return models.DefaultNotificationPreferences(syntax.DID(userDid)), nil
	}

	return p, nil
}

func GetNotificationPreferences(e Execer, filters ...filter) (map[syntax.DID]*models.NotificationPreferences, error) {
	prefsMap := make(map[syntax.DID]*models.NotificationPreferences)

	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(`
		select
			id,
			user_did,
			repo_starred,
			issue_created,
			issue_commented,
			pull_created,
			pull_commented,
			followed,
			pull_merged,
			issue_closed,
			email_notifications
		from
			notification_preferences
		%s
	`, whereClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var prefs models.NotificationPreferences
		if err := rows.Scan(
			&prefs.ID,
			&prefs.UserDid,
			&prefs.RepoStarred,
			&prefs.IssueCreated,
			&prefs.IssueCommented,
			&prefs.PullCreated,
			&prefs.PullCommented,
			&prefs.Followed,
			&prefs.PullMerged,
			&prefs.IssueClosed,
			&prefs.EmailNotifications,
		); err != nil {
			return nil, err
		}

		prefsMap[prefs.UserDid] = &prefs
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return prefsMap, nil
}

func (d *DB) UpdateNotificationPreferences(ctx context.Context, prefs *models.NotificationPreferences) error {
	query := `
		INSERT OR REPLACE INTO notification_preferences
		(user_did, repo_starred, issue_created, issue_commented, pull_created,
		 pull_commented, followed, pull_merged, issue_closed, email_notifications)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := d.DB.ExecContext(ctx, query,
		prefs.UserDid,
		prefs.RepoStarred,
		prefs.IssueCreated,
		prefs.IssueCommented,
		prefs.PullCreated,
		prefs.PullCommented,
		prefs.Followed,
		prefs.PullMerged,
		prefs.IssueClosed,
		prefs.EmailNotifications,
	)
	if err != nil {
		return fmt.Errorf("failed to update notification preferences: %w", err)
	}

	if prefs.ID == 0 {
		id, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get preferences ID: %w", err)
		}
		prefs.ID = id
	}

	return nil
}

func (d *DB) ClearOldNotifications(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	createdFilter := FilterLte("created", cutoff)

	query := fmt.Sprintf(`
		DELETE FROM notifications
		WHERE %s
	`, createdFilter.Condition())

	_, err := d.DB.ExecContext(ctx, query, createdFilter.Arg()...)
	if err != nil {
		return fmt.Errorf("failed to cleanup old notifications: %w", err)
	}

	return nil
}
