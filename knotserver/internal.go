package knotserver

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"tangled.sh/tangled.sh/core/knotserver/config"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/knotserver/notifier"
	"tangled.sh/tangled.sh/core/rbac"
)

type InternalHandle struct {
	db *db.DB
	c  *config.Config
	e  *rbac.Enforcer
	l  *slog.Logger
	n  *notifier.Notifier
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

func (h *InternalHandle) InternalKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.db.GetAllPublicKeys()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := make([]map[string]interface{}, 0)
	for _, key := range keys {
		j := key.JSON()
		data = append(data, j)
	}
	writeJSON(w, data)
	return
}

func (h *InternalHandle) PostReceiveHook(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "PostReceiveHook")

	gitAbsoluteDir := r.Header.Get("X-Git-Dir")
	gitRelativeDir, err := filepath.Rel(h.c.Repo.ScanPath, gitAbsoluteDir)
	if err != nil {
		l.Error("failed to calculate relative git dir", "scanPath", h.c.Repo.ScanPath, "gitAbsoluteDir", gitAbsoluteDir)
		return
	}
	gitUserDid := r.Header.Get("X-Git-User-Did")

	var ops []db.Op
	scanner := bufio.NewScanner(r.Body)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			l.Error("invalid payload", "parts", parts)
			continue
		}

		tid := TID()
		oldSha := parts[0]
		newSha := parts[1]
		ref := parts[2]
		op := db.Op{
			Tid:    tid,
			Did:    gitUserDid,
			Repo:   gitRelativeDir,
			OldSha: oldSha,
			NewSha: newSha,
			Ref:    ref,
		}
		ops = append(ops, op)
	}

	if err := scanner.Err(); err != nil {
		l.Error("failed to read payload", "err", err)
		return
	}

	for _, op := range ops {
		err := h.db.InsertOp(op, h.n)
		if err != nil {
			l.Error("failed to insert op", "err", err, "op", op)
			continue
		}
	}

	return
}

func Internal(ctx context.Context, c *config.Config, db *db.DB, e *rbac.Enforcer, l *slog.Logger, n *notifier.Notifier) http.Handler {
	r := chi.NewRouter()

	h := InternalHandle{
		db,
		c,
		e,
		l,
		n,
	}

	r.Get("/push-allowed", h.PushAllowed)
	r.Get("/keys", h.InternalKeys)
	r.Post("/hooks/post-receive", h.PostReceiveHook)
	r.Mount("/debug", middleware.Profiler())

	return r
}
