package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotserver/config"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/workflow"
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

	parts := strings.SplitN(gitRelativeDir, "/", 2)
	if len(parts) != 2 {
		l.Error("invalid git dir", "gitRelativeDir", gitRelativeDir)
		return
	}
	repoDid := parts[0]
	repoName := parts[1]

	gitUserDid := r.Header.Get("X-Git-User-Did")

	lines, err := git.ParsePostReceive(r.Body)
	if err != nil {
		l.Error("failed to parse post-receive payload", "err", err)
		// non-fatal
	}

	for _, line := range lines {
		err := h.insertRefUpdate(line, gitUserDid, repoDid, repoName)
		if err != nil {
			l.Error("failed to insert op", "err", err, "line", line, "did", gitUserDid, "repo", gitRelativeDir)
			// non-fatal
		}

		err = h.triggerPipeline(line, gitUserDid, repoDid, repoName)
		if err != nil {
			l.Error("failed to trigger pipeline", "err", err, "line", line, "did", gitUserDid, "repo", gitRelativeDir)
			// non-fatal
		}
	}
}

func (h *InternalHandle) insertRefUpdate(line git.PostReceiveLine, gitUserDid, repoDid, repoName string) error {
	didSlashRepo, err := securejoin.SecureJoin(repoDid, repoName)
	if err != nil {
		return err
	}

	repoPath, err := securejoin.SecureJoin(h.c.Repo.ScanPath, didSlashRepo)
	if err != nil {
		return err
	}

	gr, err := git.Open(repoPath, line.Ref)
	if err != nil {
		return fmt.Errorf("failed to open git repo at ref %s: %w", line.Ref, err)
	}

	meta := gr.RefUpdateMeta(line)

	metaRecord := meta.AsRecord()

	refUpdate := tangled.GitRefUpdate{
		OldSha:       line.OldSha.String(),
		NewSha:       line.NewSha.String(),
		Ref:          line.Ref,
		CommitterDid: gitUserDid,
		RepoDid:      repoDid,
		RepoName:     repoName,
		Meta:         &metaRecord,
	}
	eventJson, err := json.Marshal(refUpdate)
	if err != nil {
		return err
	}

	event := db.Event{
		Rkey:      TID(),
		Nsid:      tangled.GitRefUpdateNSID,
		EventJson: string(eventJson),
	}

	return h.db.InsertEvent(event, h.n)
}

func (h *InternalHandle) triggerPipeline(line git.PostReceiveLine, gitUserDid, repoDid, repoName string) error {
	didSlashRepo, err := securejoin.SecureJoin(repoDid, repoName)
	if err != nil {
		return err
	}

	repoPath, err := securejoin.SecureJoin(h.c.Repo.ScanPath, didSlashRepo)
	if err != nil {
		return err
	}

	gr, err := git.Open(repoPath, line.Ref)
	if err != nil {
		return err
	}

	workflowDir, err := gr.FileTree(context.Background(), workflow.WorkflowDir)
	if err != nil {
		return err
	}

	var pipeline workflow.Pipeline
	for _, e := range workflowDir {
		if !e.IsFile {
			continue
		}

		fpath := filepath.Join(workflow.WorkflowDir, e.Name)
		contents, err := gr.RawContent(fpath)
		if err != nil {
			continue
		}

		wf, err := workflow.FromFile(e.Name, contents)
		if err != nil {
			// TODO: log here, respond to client that is pushing
			h.l.Error("failed to parse workflow", "err", err, "path", fpath)
			continue
		}

		pipeline = append(pipeline, wf)
	}

	trigger := tangled.Pipeline_PushTriggerData{
		Ref:    line.Ref,
		OldSha: line.OldSha.String(),
		NewSha: line.NewSha.String(),
	}

	compiler := workflow.Compiler{
		Trigger: tangled.Pipeline_TriggerMetadata{
			Kind: string(workflow.TriggerKindPush),
			Push: &trigger,
			Repo: &tangled.Pipeline_TriggerRepo{
				Did:  repoDid,
				Knot: h.c.Server.Hostname,
				Repo: repoName,
			},
		},
	}

	// TODO: send the diagnostics back to the user here via stderr
	cp := compiler.Compile(pipeline)
	eventJson, err := json.Marshal(cp)
	if err != nil {
		return err
	}

	// do not run empty pipelines
	if cp.Workflows == nil {
		return nil
	}

	event := db.Event{
		Rkey:      TID(),
		Nsid:      tangled.PipelineNSID,
		EventJson: string(eventJson),
	}

	return h.db.InsertEvent(event, h.n)
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
