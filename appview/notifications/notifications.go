package notifications

import (
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/middleware"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pagination"
)

type Notifications struct {
	db    *db.DB
	oauth *oauth.OAuth
	pages *pages.Pages
}

func New(database *db.DB, oauthHandler *oauth.OAuth, pagesHandler *pages.Pages) *Notifications {
	return &Notifications{
		db:    database,
		oauth: oauthHandler,
		pages: pagesHandler,
	}
}

func (n *Notifications) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.AuthMiddleware(n.oauth))

	r.Get("/", n.notificationsPage)

	r.Get("/count", n.getUnreadCount)
	r.Post("/{id}/read", n.markRead)
	r.Post("/read-all", n.markAllRead)
	r.Delete("/{id}", n.deleteNotification)

	return r
}

func (n *Notifications) notificationsPage(w http.ResponseWriter, r *http.Request) {
	userDid := n.oauth.GetDid(r)

	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 20 // default
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0 // default
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	page := pagination.Page{Limit: limit + 1, Offset: offset}
	notifications, err := db.GetNotificationsWithEntities(n.db, page, db.FilterEq("recipient_did", userDid))
	if err != nil {
		log.Println("failed to get notifications:", err)
		n.pages.Error500(w)
		return
	}

	hasMore := len(notifications) > limit
	if hasMore {
		notifications = notifications[:limit]
	}

	err = n.db.MarkAllNotificationsRead(r.Context(), userDid)
	if err != nil {
		log.Println("failed to mark notifications as read:", err)
	}

	unreadCount := 0

	user := n.oauth.GetUser(r)
	if user == nil {
		http.Error(w, "Failed to get user", http.StatusInternalServerError)
		return
	}

	params := pages.NotificationsParams{
		LoggedInUser:  user,
		Notifications: notifications,
		UnreadCount:   unreadCount,
		HasMore:       hasMore,
		NextOffset:    offset + limit,
		Limit:         limit,
	}

	err = n.pages.Notifications(w, params)
	if err != nil {
		log.Println("failed to load notifs:", err)
		n.pages.Error500(w)
		return
	}
}

func (n *Notifications) getUnreadCount(w http.ResponseWriter, r *http.Request) {
	userDid := n.oauth.GetDid(r)

	count, err := n.db.GetUnreadNotificationCount(r.Context(), userDid)
	if err != nil {
		http.Error(w, "Failed to get unread count", http.StatusInternalServerError)
		return
	}

	params := pages.NotificationCountParams{
		Count: count,
	}
	err = n.pages.NotificationCount(w, params)
	if err != nil {
		http.Error(w, "Failed to render count", http.StatusInternalServerError)
		return
	}
}

func (n *Notifications) markRead(w http.ResponseWriter, r *http.Request) {
	userDid := n.oauth.GetDid(r)

	idStr := chi.URLParam(r, "id")
	notificationID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid notification ID", http.StatusBadRequest)
		return
	}

	err = n.db.MarkNotificationRead(r.Context(), notificationID, userDid)
	if err != nil {
		http.Error(w, "Failed to mark notification as read", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (n *Notifications) markAllRead(w http.ResponseWriter, r *http.Request) {
	userDid := n.oauth.GetDid(r)

	err := n.db.MarkAllNotificationsRead(r.Context(), userDid)
	if err != nil {
		http.Error(w, "Failed to mark all notifications as read", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

func (n *Notifications) deleteNotification(w http.ResponseWriter, r *http.Request) {
	userDid := n.oauth.GetDid(r)

	idStr := chi.URLParam(r, "id")
	notificationID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid notification ID", http.StatusBadRequest)
		return
	}

	err = n.db.DeleteNotification(r.Context(), notificationID, userDid)
	if err != nil {
		http.Error(w, "Failed to delete notification", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
