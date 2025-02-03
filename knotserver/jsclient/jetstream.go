package jsclient

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type JetstreamClient struct {
	collections []string
	dids        []string
	conn        *websocket.Conn
	mu          sync.RWMutex
	reconnectCh chan struct{}
}

func NewJetstreamClient(collections, dids []string) *JetstreamClient {
	return &JetstreamClient{
		collections: collections,
		dids:        dids,
		reconnectCh: make(chan struct{}, 1),
	}
}

func (j *JetstreamClient) buildWebsocketURL(queryParams string) url.URL {

	u := url.URL{
		Scheme:   "wss",
		Host:     "jetstream1.us-west.bsky.network",
		Path:     "/subscribe",
		RawQuery: queryParams,
	}

	return u
}

// UpdateCollections updates the collections list and triggers a reconnection
func (j *JetstreamClient) UpdateCollections(collections []string) {
	j.mu.Lock()
	j.collections = collections
	j.mu.Unlock()
	j.triggerReconnect()
}

// UpdateDids updates the Dids list and triggers a reconnection
func (j *JetstreamClient) UpdateDids(dids []string) {
	j.mu.Lock()
	j.dids = dids
	j.mu.Unlock()
	j.triggerReconnect()
}

func (j *JetstreamClient) triggerReconnect() {
	select {
	case j.reconnectCh <- struct{}{}:
	default:
		// Channel already has a pending reconnect
	}
}

func (j *JetstreamClient) buildQueryParams(cursor int64) string {
	j.mu.RLock()
	defer j.mu.RUnlock()

	var collections, dids string
	if len(j.collections) > 0 {
		collections = fmt.Sprintf("wantedCollections=%s&cursor=%d", j.collections[0], cursor)
		for _, collection := range j.collections[1:] {
			collections += fmt.Sprintf("&wantedCollections=%s", collection)
		}
	}
	if len(j.dids) > 0 {
		for i, did := range j.dids {
			if i == 0 {
				dids = fmt.Sprintf("wantedDids=%s", did)
			} else {
				dids += fmt.Sprintf("&wantedDids=%s", did)
			}
		}
	}

	var queryStr string
	if collections != "" && dids != "" {
		queryStr = collections + "&" + dids
	} else if collections != "" {
		queryStr = collections
	} else if dids != "" {
		queryStr = dids
	}

	return queryStr
}

func (j *JetstreamClient) connect(cursor int64) error {
	queryParams := j.buildQueryParams(cursor)
	u := j.buildWebsocketURL(queryParams)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}

	if j.conn != nil {
		j.conn.Close()
	}
	j.conn = conn
	return nil
}

func (j *JetstreamClient) readMessages(ctx context.Context, messages chan []byte) {
	defer close(messages)
	defer j.conn.Close()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-j.reconnectCh:
			// Reconnect with new parameters
			cursor := time.Now().Add(-5 * time.Second).UnixMicro()
			if err := j.connect(cursor); err != nil {
				log.Printf("error reconnecting to jetstream: %v", err)
				return
			}
		case <-ticker.C:
			_, message, err := j.conn.ReadMessage()
			if err != nil {
				log.Printf("error reading from websocket: %v", err)
				return
			}
			messages <- message
		}
	}
}

func (j *JetstreamClient) ReadJetstream(ctx context.Context) (chan []byte, error) {
	fiveSecondsAgo := time.Now().Add(-5 * time.Second).UnixMicro()

	if err := j.connect(fiveSecondsAgo); err != nil {
		log.Printf("error connecting to jetstream: %v", err)
		return nil, err
	}

	messages := make(chan []byte)
	go j.readMessages(ctx, messages)

	return messages, nil
}
