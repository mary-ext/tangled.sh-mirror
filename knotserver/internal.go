package knotserver

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/rbac"
)

type InternalHandle struct {
	db *db.DB
	e  *rbac.Enforcer
}

func (h *InternalHandle) PushAllowed(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	repo := r.URL.Query().Get("repo")

	if user == "" || repo == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ok, err := h.e.IsPushAllowed(user, ThisServer, repo)
	if err != nil || !ok {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	return
}

func Internal(ctx context.Context, db *db.DB, e *rbac.Enforcer) http.Handler {
	r := chi.NewRouter()

	h := InternalHandle{
		db,
		e,
	}

	r.Get("/push-allowed", h.PushAllowed)

	return r
}
