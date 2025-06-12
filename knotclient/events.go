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

	"tangled.sh/tangled.sh/core/knotclient/cursor"
	"tangled.sh/tangled.sh/core/log"

	"github.com/gorilla/websocket"
)

type ProcessFunc func(ctx context.Context, source EventSource, message Message) error

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
	CursorStore       cursor.Store
}

func NewConsumerConfig() *ConsumerConfig {
	return &ConsumerConfig{
		Sources: make(map[EventSource]struct{}),
	}
}

func (cc *ConsumerConfig) AddEventSource(es EventSource) {
	cc.Sources[es] = struct{}{}
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
	wg         sync.WaitGroup
	dialer     *websocket.Dialer
	connMap    sync.Map
	jobQueue   chan job
	logger     *slog.Logger
	randSource *rand.Rand

	// rw lock over edits to ConsumerConfig
	cfgMu sync.RWMutex
	cfg   ConsumerConfig
}

func (e *EventConsumer) buildUrl(s EventSource, cursor int64) (*url.URL, error) {
	scheme := "wss"
	if e.cfg.Dev {
		scheme = "ws"
	}

	u, err := url.Parse(scheme + "://" + s.Knot + "/events")
	if err != nil {
		return nil, err
	}

	if cursor != 0 {
		query := url.Values{}
		query.Add("cursor", fmt.Sprintf("%d", cursor))
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
		cfg.CursorStore = &cursor.MemoryStore{}
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
	c.cfgMu.Lock()
	c.cfg.Sources[s] = struct{}{}
	c.wg.Add(1)
	go c.startConnectionLoop(ctx, s)
	c.cfgMu.Unlock()
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
			c.cfg.CursorStore.Set(j.source.Knot, time.Now().Unix())

			if err := c.cfg.ProcessFunc(ctx, j.source, msg); err != nil {
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
