package spindle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"tangled.sh/tangled.sh/core/spindle/engine"
	"tangled.sh/tangled.sh/core/spindle/models"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/hpcloud/tail"
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

	l := s.l.With("handler", "Logs")
	l = s.l.With("wid", wid)

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

	if err := s.streamLogsFromDisk(ctx, conn, wid); err != nil {
		l.Error("log stream failed", "err", err)
	}
	l.Debug("logs connection closed")
}

func (s *Spindle) streamLogsFromDisk(ctx context.Context, conn *websocket.Conn, wid models.WorkflowId) error {
	filePath := engine.LogFilePath(s.cfg.Pipelines.LogDir, wid)

	config := tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: false,
		Location:  &tail.SeekInfo{Offset: 0, Whence: 0},
		Logger:    tail.DiscardingLogger,
	}

	t, err := tail.TailFile(filePath, config)
	if err != nil {
		return fmt.Errorf("failed to tail log file: %w", err)
	}
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line := <-t.Lines:
			if line == nil {
				return fmt.Errorf("tail channel closed unexpectedly")
			}

			if line.Err != nil {
				return fmt.Errorf("error tailing log file: %w", line.Err)
			}

			if err := conn.WriteMessage(websocket.TextMessage, []byte(line.Text)); err != nil {
				return fmt.Errorf("failed to write to websocket: %w", err)
			}
		}
	}
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
