package knotserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func (h *Handle) Events(w http.ResponseWriter, r *http.Request) {
	l := h.l.With("handler", "OpLog")
	l.Debug("received new connection")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Error("websocket upgrade failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	l.Debug("upgraded http to wss")

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
	if err := h.streamOps(conn, &cursor); err != nil {
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
			if err := h.streamOps(conn, &cursor); err != nil {
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

func (h *Handle) streamOps(conn *websocket.Conn, cursor *int64) error {
	events, err := h.db.GetEvents(*cursor)
	if err != nil {
		h.l.Error("failed to fetch events from db", "err", err, "cursor", cursor)
		return err
	}
	h.l.Debug("ops", "ops", events)

	for _, event := range events {
		// first extract the inner json into a map
		var eventJson map[string]any
		err := json.Unmarshal([]byte(event.EventJson), &eventJson)
		if err != nil {
			h.l.Error("failed to unmarshal event", "err", err)
			return err
		}

		jsonMsg, err := json.Marshal(map[string]any{
			"rkey":  event.Rkey,
			"nsid":  event.Nsid,
			"event": eventJson,
		})
		if err != nil {
			h.l.Error("failed to marshal record", "err", err)
			return err
		}

		if err := conn.WriteMessage(websocket.TextMessage, jsonMsg); err != nil {
			h.l.Debug("err", "err", err)
			return err
		}
		*cursor = event.Created
	}

	return nil
}
