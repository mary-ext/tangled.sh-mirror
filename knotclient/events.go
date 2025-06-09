package knotclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"sync"
	"time"

	"tangled.sh/tangled.sh/core/appview/cache"
	"tangled.sh/tangled.sh/core/log"

	"github.com/gorilla/websocket"
)

type ProcessFunc func(source EventSource, message Message) error

type Message struct {
	Rkey string
	Nsid string
	// do not full deserialize this portion of the message, processFunc can do that
	EventJson json.RawMessage `json:"event"`
}

type ConsumerConfig struct {
	Sources           map[EventSource]struct{}
	ProcessFunc       ProcessFunc
	RetryInterval     time.Duration
	MaxRetryInterval  time.Duration
	ConnectionTimeout time.Duration
	WorkerCount       int
	QueueSize         int
	Logger            *slog.Logger
	Dev               bool
	CursorStore       CursorStore
}

type EventSource struct {
	Knot string
}

func NewEventSource(knot string) EventSource {
	return EventSource{
		Knot: knot,
	}
}

type EventConsumer struct {
	cfg        ConsumerConfig
	wg         sync.WaitGroup
	dialer     *websocket.Dialer
	connMap    sync.Map
	jobQueue   chan job
	logger     *slog.Logger
	randSource *rand.Rand

	// rw lock over edits to consumer config
	mu sync.RWMutex
}

type CursorStore interface {
	Set(knot, cursor string)
	Get(knot string) (cursor string)
}

type RedisCursorStore struct {
	rdb *cache.Cache
}

func NewRedisCursorStore(cache *cache.Cache) RedisCursorStore {
	return RedisCursorStore{
		rdb: cache,
	}
}

const (
	cursorKey = "cursor:%s"
)

func (r *RedisCursorStore) Set(knot, cursor string) {
	key := fmt.Sprintf(cursorKey, knot)
	r.rdb.Set(context.Background(), key, cursor, 0)
}

func (r *RedisCursorStore) Get(knot string) (cursor string) {
	key := fmt.Sprintf(cursorKey, knot)
	val, err := r.rdb.Get(context.Background(), key).Result()
	if err != nil {
		return ""
	}

	return val
}

type MemoryCursorStore struct {
	store sync.Map
}

func (m *MemoryCursorStore) Set(knot, cursor string) {
	m.store.Store(knot, cursor)
}

func (m *MemoryCursorStore) Get(knot string) (cursor string) {
	if result, ok := m.store.Load(knot); ok {
		if val, ok := result.(string); ok {
			return val
		}
	}

	return ""
}

func (e *EventConsumer) buildUrl(s EventSource, cursor string) (*url.URL, error) {
	scheme := "wss"
	if e.cfg.Dev {
		scheme = "ws"
	}

	u, err := url.Parse(scheme + "://" + s.Knot + "/events")
	if err != nil {
		return nil, err
	}

	if cursor != "" {
		query := url.Values{}
		query.Add("cursor", cursor)
		u.RawQuery = query.Encode()
	}
	return u, nil
}

type job struct {
	source  EventSource
	message []byte
}

func NewEventConsumer(cfg ConsumerConfig) *EventConsumer {
	if cfg.RetryInterval == 0 {
		cfg.RetryInterval = 15 * time.Minute
	}
	if cfg.ConnectionTimeout == 0 {
		cfg.ConnectionTimeout = 10 * time.Second
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 5
	}
	if cfg.MaxRetryInterval == 0 {
		cfg.MaxRetryInterval = 1 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New("eventconsumer")
	}
	if cfg.QueueSize == 0 {
		cfg.QueueSize = 100
	}
	if cfg.CursorStore == nil {
		cfg.CursorStore = &MemoryCursorStore{}
	}
	return &EventConsumer{
		cfg:        cfg,
		dialer:     websocket.DefaultDialer,
		jobQueue:   make(chan job, cfg.QueueSize), // buffered job queue
		logger:     cfg.Logger,
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (c *EventConsumer) Start(ctx context.Context) {
	c.cfg.Logger.Info("starting consumer", "config", c.cfg)

	// start workers
	for range c.cfg.WorkerCount {
		c.wg.Add(1)
		go c.worker(ctx)
	}

	// start streaming
	for source := range c.cfg.Sources {
		c.wg.Add(1)
		go c.startConnectionLoop(ctx, source)
	}
}

func (c *EventConsumer) Stop() {
	c.connMap.Range(func(_, val any) bool {
		if conn, ok := val.(*websocket.Conn); ok {
			conn.Close()
		}
		return true
	})
	c.wg.Wait()
	close(c.jobQueue)
}

func (c *EventConsumer) AddSource(ctx context.Context, s EventSource) {
	c.mu.Lock()
	c.cfg.Sources[s] = struct{}{}
	c.wg.Add(1)
	go c.startConnectionLoop(ctx, s)
	c.mu.Unlock()
}

func (c *EventConsumer) worker(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-c.jobQueue:
			if !ok {
				return
			}

			var msg Message
			err := json.Unmarshal(j.message, &msg)
			if err != nil {
				c.logger.Error("error deserializing message", "source", j.source.Knot, "err", err)
				return
			}

			// update cursor
			c.cfg.CursorStore.Set(j.source.Knot, msg.Rkey)

			if err := c.cfg.ProcessFunc(j.source, msg); err != nil {
				c.logger.Error("error processing message", "source", j.source, "err", err)
			}
		}
	}
}

func (c *EventConsumer) startConnectionLoop(ctx context.Context, source EventSource) {
	defer c.wg.Done()
	retryInterval := c.cfg.RetryInterval
	for {
		select {
		case <-ctx.Done():
			return
		default:
			err := c.runConnection(ctx, source)
			if err != nil {
				c.logger.Error("connection failed", "source", source, "err", err)
			}

			// apply jitter
			jitter := time.Duration(c.randSource.Int63n(int64(retryInterval) / 5))
			delay := retryInterval + jitter

			if retryInterval < c.cfg.MaxRetryInterval {
				retryInterval *= 2
				if retryInterval > c.cfg.MaxRetryInterval {
					retryInterval = c.cfg.MaxRetryInterval
				}
			}
			c.logger.Info("retrying connection", "source", source, "delay", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (c *EventConsumer) runConnection(ctx context.Context, source EventSource) error {
	connCtx, cancel := context.WithTimeout(ctx, c.cfg.ConnectionTimeout)
	defer cancel()

	cursor := c.cfg.CursorStore.Get(source.Knot)

	u, err := c.buildUrl(source, cursor)
	if err != nil {
		return err
	}

	c.logger.Info("connecting", "url", u.String())
	conn, _, err := c.dialer.DialContext(connCtx, u.String(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	c.connMap.Store(source, conn)
	defer c.connMap.Delete(source)

	c.logger.Info("connected", "source", source)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			if msgType != websocket.TextMessage {
				continue
			}
			select {
			case c.jobQueue <- job{source: source, message: msg}:
			case <-ctx.Done():
				return nil
			}
		}
	}
}
