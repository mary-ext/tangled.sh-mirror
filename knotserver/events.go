package knotserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func (h *Handle) OpLog(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "OpLog")
	l.Info("received new connection")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Error("websocket upgrade failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	l.Info("upgraded http to wss")

	ch := h.n.Subscribe()
	defer h.n.Unsubscribe(ch)

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
	if err := h.streamOps(conn, &cursor); err != nil {
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
			if err := h.streamOps(conn, &cursor); err != nil {
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

func (h *Handle) streamOps(conn *websocket.Conn, cursor *string) error {
	ops, err := h.db.GetOps(*cursor)
	if err != nil {
		h.l.Debug("err", "err", err)
		return err
	}
	h.l.Debug("ops", "ops", ops)

	for _, op := range ops {
		if err := conn.WriteJSON(op); err != nil {
			h.l.Debug("err", "err", err)
			return err
		}
		*cursor = op.Tid
	}

	return nil
}
