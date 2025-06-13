package spindle

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

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
	l := s.l.With("handler", "Logs")

	knot := chi.URLParam(r, "knot")
	if knot == "" {
		http.Error(w, "knot required", http.StatusBadRequest)
		return
	}

	rkey := chi.URLParam(r, "rkey")
	if rkey == "" {
		http.Error(w, "rkey required", http.StatusBadRequest)
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	wid := models.WorkflowId{
		PipelineId: models.PipelineId{
			Knot: knot,
			Rkey: rkey,
		},
		Name: name,
	}

	l = l.With("knot", knot, "rkey", rkey, "name", name)

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

	if err := s.streamLogs(ctx, conn, wid); err != nil {
		l.Error("streamLogs failed", "err", err)
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

func (s *Spindle) streamPipelines(conn *websocket.Conn, cursor *int64) error {
	ops, err := s.db.GetEvents(*cursor)
	if err != nil {
		s.l.Debug("err", "err", err)
		return err
	}
	s.l.Debug("ops", "ops", ops)

	for _, op := range ops {
		if err := conn.WriteJSON(op); err != nil {
			s.l.Debug("err", "err", err)
			return err
		}
		*cursor = op.Created
	}

	return nil
}
