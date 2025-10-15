package notifications

import (
	"log/slog"
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
	db     *db.DB
	oauth  *oauth.OAuth
	pages  *pages.Pages
	logger *slog.Logger
}

func New(database *db.DB, oauthHandler *oauth.OAuth, pagesHandler *pages.Pages, logger *slog.Logger) *Notifications {
	return &Notifications{
		db:     database,
		oauth:  oauthHandler,
		pages:  pagesHandler,
		logger: logger,
	}
}

func (n *Notifications) Router(mw *middleware.Middleware) http.Handler {
	r := chi.NewRouter()

	r.Get("/count", n.getUnreadCount)

	r.Group(func(r chi.Router) {
		r.Use(middleware.AuthMiddleware(n.oauth))
		r.With(middleware.Paginate).Get("/", n.notificationsPage)
		r.Post("/{id}/read", n.markRead)
		r.Post("/read-all", n.markAllRead)
		r.Delete("/{id}", n.deleteNotification)
	})

	return r
}

func (n *Notifications) notificationsPage(w http.ResponseWriter, r *http.Request) {
	l := n.logger.With("handler", "notificationsPage")
	user := n.oauth.GetUser(r)

	page, ok := r.Context().Value("page").(pagination.Page)
	if !ok {
		l.Error("failed to get page")
		page = pagination.FirstPage()
	}

	total, err := db.CountNotifications(
		n.db,
		db.FilterEq("recipient_did", user.Did),
	)
	if err != nil {
		l.Error("failed to get total notifications", "err", err)
		n.pages.Error500(w)
		return
	}

	notifications, err := db.GetNotificationsWithEntities(
		n.db,
		page,
		db.FilterEq("recipient_did", user.Did),
	)
	if err != nil {
		l.Error("failed to get notifications", "err", err)
		n.pages.Error500(w)
		return
	}

	err = db.MarkAllNotificationsRead(n.db, user.Did)
	if err != nil {
		l.Error("failed to mark notifications as read", "err", err)
	}

	unreadCount := 0

	n.pages.Notifications(w, pages.NotificationsParams{
		LoggedInUser:  user,
		Notifications: notifications,
		UnreadCount:   unreadCount,
		Page:          page,
		Total:         total,
	})
}

func (n *Notifications) getUnreadCount(w http.ResponseWriter, r *http.Request) {
	user := n.oauth.GetUser(r)
	if user == nil {
		return
	}

	count, err := db.CountNotifications(
		n.db,
		db.FilterEq("recipient_did", user.Did),
		db.FilterEq("read", 0),
	)
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

	err = db.MarkNotificationRead(n.db, notificationID, userDid)
	if err != nil {
		http.Error(w, "Failed to mark notification as read", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (n *Notifications) markAllRead(w http.ResponseWriter, r *http.Request) {
	userDid := n.oauth.GetDid(r)

	err := db.MarkAllNotificationsRead(n.db, userDid)
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

	err = db.DeleteNotification(n.db, notificationID, userDid)
	if err != nil {
		http.Error(w, "Failed to delete notification", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
