package spindle

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tangled.sh/tangled.sh/core/spindle/engine"
	"tangled.sh/tangled.sh/core/spindle/models"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func (s *Spindle) Events(w http.ResponseWriter, r *http.Request) {
	l := s.l.With("handler", "Events")
	l.Debug("received new connection")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Error("websocket upgrade failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	l.Debug("upgraded http to wss")

	ch := s.n.Subscribe()
	defer s.n.Unsubscribe(ch)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				l.Error("failed to read", "err", err)
				cancel()
				return
			}
		}
	}()

	defaultCursor := time.Now().UnixNano()
	cursorStr := r.URL.Query().Get("cursor")
	cursor, err := strconv.ParseInt(cursorStr, 10, 64)
	if err != nil {
		l.Error("empty or invalid cursor", "invalidCursor", cursorStr, "default", defaultCursor)
	}
	if cursor == 0 {
		cursor = defaultCursor
	}

	// complete backfill first before going to live data
	l.Debug("going through backfill", "cursor", cursor)
	if err := s.streamPipelines(conn, &cursor); err != nil {
		l.Error("failed to backfill", "err", err)
		return
	}

	for {
		// wait for new data or timeout
		select {
		case <-ctx.Done():
			l.Debug("stopping stream: client closed connection")
			return
		case <-ch:
			// we have been notified of new data
			l.Debug("going through live data", "cursor", cursor)
			if err := s.streamPipelines(conn, &cursor); err != nil {
				l.Error("failed to stream", "err", err)
				return
			}
		case <-time.After(30 * time.Second):
			// send a keep-alive
			l.Debug("sent keepalive")
			if err = conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second)); err != nil {
				l.Error("failed to write control", "err", err)
			}
		}
	}
}

func (s *Spindle) Logs(w http.ResponseWriter, r *http.Request) {
	wid, err := getWorkflowID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.handleLogStream(w, r, func(ctx context.Context, conn *websocket.Conn) error {
		return s.streamLogs(ctx, conn, wid)
	})
}

func (s *Spindle) StepLogs(w http.ResponseWriter, r *http.Request) {
	wid, err := getWorkflowID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	idxStr := chi.URLParam(r, "idx")
	if idxStr == "" {
		http.Error(w, "step index required", http.StatusBadRequest)
		return
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		http.Error(w, "bad step index", http.StatusBadRequest)
		return
	}

	s.handleLogStream(w, r, func(ctx context.Context, conn *websocket.Conn) error {
		return s.streamLogFromDisk(ctx, conn, wid, idx)
	})
}

func (s *Spindle) handleLogStream(w http.ResponseWriter, r *http.Request, streamFn func(ctx context.Context, conn *websocket.Conn) error) {
	l := s.l.With("handler", "Logs")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Error("websocket upgrade failed", "err", err)
		http.Error(w, "failed to upgrade", http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	l.Debug("upgraded http to wss")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				l.Debug("client disconnected", "err", err)
				cancel()
				return
			}
		}
	}()

	if err := streamFn(ctx, conn); err != nil {
		l.Error("log stream failed", "err", err)
	}
	l.Debug("logs connection closed")
}

func (s *Spindle) streamLogs(ctx context.Context, conn *websocket.Conn, wid models.WorkflowId) error {
	l := s.l.With("workflow_id", wid.String())

	stdoutCh, stderrCh, ok := s.eng.LogChannels(wid)
	if !ok {
		return fmt.Errorf("workflow_id %q not found", wid.String())
	}

	done := make(chan struct{})

	go func() {
		for {
			select {
			case line, ok := <-stdoutCh:
				if !ok {
					done <- struct{}{}
					return
				}
				msg := map[string]string{"type": "stdout", "data": line}
				if err := conn.WriteJSON(msg); err != nil {
					l.Error("write stdout failed", "err", err)
					done <- struct{}{}
					return
				}
			case <-ctx.Done():
				done <- struct{}{}
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case line, ok := <-stderrCh:
				if !ok {
					done <- struct{}{}
					return
				}
				msg := map[string]string{"type": "stderr", "data": line}
				if err := conn.WriteJSON(msg); err != nil {
					l.Error("write stderr failed", "err", err)
					done <- struct{}{}
					return
				}
			case <-ctx.Done():
				done <- struct{}{}
				return
			}
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}

	return nil
}

func (s *Spindle) streamLogFromDisk(ctx context.Context, conn *websocket.Conn, wid models.WorkflowId, stepIdx int) error {
	streams := []string{"stdout", "stderr"}

	for _, stream := range streams {
		data, err := engine.ReadStepLog(s.cfg.Pipelines.LogDir, wid.String(), stream, stepIdx)
		if err != nil {
			// log but continue to next stream
			s.l.Error("failed to read step log", "stream", stream, "step", stepIdx, "wid", wid.String(), "err", err)
			continue
		}

		scanner := bufio.NewScanner(strings.NewReader(data))
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				msg := map[string]string{
					"type": stream,
					"data": scanner.Text(),
				}
				if err := conn.WriteJSON(msg); err != nil {
					return err
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return fmt.Errorf("error scanning %s log: %w", stream, err)
		}
	}

	return nil
}

func (s *Spindle) streamPipelines(conn *websocket.Conn, cursor *int64) error {
	events, err := s.db.GetEvents(*cursor)
	if err != nil {
		s.l.Debug("err", "err", err)
		return err
	}
	s.l.Debug("ops", "ops", events)

	for _, event := range events {
		// first extract the inner json into a map
		var eventJson map[string]any
		err := json.Unmarshal([]byte(event.EventJson), &eventJson)
		if err != nil {
			s.l.Error("failed to unmarshal event", "err", err)
			return err
		}

		jsonMsg, err := json.Marshal(map[string]any{
			"rkey":  event.Rkey,
			"nsid":  event.Nsid,
			"event": eventJson,
		})
		if err != nil {
			s.l.Error("failed to marshal record", "err", err)
			return err
		}

		if err := conn.WriteMessage(websocket.TextMessage, jsonMsg); err != nil {
			s.l.Debug("err", "err", err)
			return err
		}
		*cursor = event.Created
	}

	return nil
}

func getWorkflowID(r *http.Request) (models.WorkflowId, error) {
	knot := chi.URLParam(r, "knot")
	rkey := chi.URLParam(r, "rkey")
	name := chi.URLParam(r, "name")

	if knot == "" || rkey == "" || name == "" {
		return models.WorkflowId{}, fmt.Errorf("missing required parameters")
	}

	return models.WorkflowId{
		PipelineId: models.PipelineId{
			Knot: knot,
			Rkey: rkey,
		},
		Name: name,
	}, nil
}
