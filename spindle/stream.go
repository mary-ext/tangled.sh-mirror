package spindle

import (
	"fmt"
	"net/http"
	"time"

	"context"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func (s *Spindle) Events(w http.ResponseWriter, r *http.Request) {
	l := s.l.With("handler", "Events")
	l.Info("received new connection")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Error("websocket upgrade failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	l.Info("upgraded http to wss")

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

	cursor := ""

	// complete backfill first before going to live data
	l.Info("going through backfill", "cursor", cursor)
	if err := s.streamPipelines(conn, &cursor); err != nil {
		l.Error("failed to backfill", "err", err)
		return
	}

	for {
		// wait for new data or timeout
		select {
		case <-ctx.Done():
			l.Info("stopping stream: client closed connection")
			return
		case <-ch:
			// we have been notified of new data
			l.Info("going through live data", "cursor", cursor)
			if err := s.streamPipelines(conn, &cursor); err != nil {
				l.Error("failed to stream", "err", err)
				return
			}
		case <-time.After(30 * time.Second):
			// send a keep-alive
			l.Info("sent keepalive")
			if err = conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second)); err != nil {
				l.Error("failed to write control", "err", err)
			}
		}
	}
}

func (s *Spindle) Logs(w http.ResponseWriter, r *http.Request) {
	l := s.l.With("handler", "Logs")

	pipelineID := chi.URLParam(r, "pipelineID")
	if pipelineID == "" {
		http.Error(w, "pipelineID required", http.StatusBadRequest)
		return
	}
	l = l.With("pipelineID", pipelineID)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Error("websocket upgrade failed", "err", err)
		http.Error(w, "failed to upgrade", http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	l.Info("upgraded http to wss")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				l.Info("client disconnected", "err", err)
				cancel()
				return
			}
		}
	}()

	if err := s.streamLogs(ctx, conn, pipelineID); err != nil {
		l.Error("streamLogs failed", "err", err)
	}
	l.Info("logs connection closed")
}

func (s *Spindle) streamLogs(ctx context.Context, conn *websocket.Conn, pipelineID string) error {
	l := s.l.With("pipelineID", pipelineID)

	stdoutCh, stderrCh, ok := s.eng.LogChannels(pipelineID)
	if !ok {
		return fmt.Errorf("pipelineID %q not found", pipelineID)
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

func (s *Spindle) streamPipelines(conn *websocket.Conn, cursor *string) error {
	ops, err := s.db.GetPipelineStatusAsRecords(*cursor)
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
		*cursor = op.Rkey
	}

	return nil
}
