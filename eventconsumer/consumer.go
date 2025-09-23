package eventconsumer

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"net/url"
	"sync"
	"time"

	"tangled.org/core/eventconsumer/cursor"
	"tangled.org/core/log"

	"github.com/avast/retry-go/v4"
	"github.com/gorilla/websocket"
)

type ProcessFunc func(ctx context.Context, source Source, message Message) error

type Message struct {
	Rkey string
	Nsid string
	// do not full deserialize this portion of the message, processFunc can do that
	EventJson json.RawMessage `json:"event"`
}

type ConsumerConfig struct {
	Sources           map[Source]struct{}
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
		Sources: make(map[Source]struct{}),
	}
}

type Source interface {
	// url to start streaming events from
	Url(cursor int64, dev bool) (*url.URL, error)
	// cache key for cursor storage
	Key() string
}

type Consumer struct {
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

type job struct {
	source  Source
	message []byte
}

func NewConsumer(cfg ConsumerConfig) *Consumer {
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
		cfg.Logger = log.New("consumer")
	}
	if cfg.QueueSize == 0 {
		cfg.QueueSize = 100
	}
	if cfg.CursorStore == nil {
		cfg.CursorStore = &cursor.MemoryStore{}
	}
	return &Consumer{
		cfg:        cfg,
		dialer:     websocket.DefaultDialer,
		jobQueue:   make(chan job, cfg.QueueSize), // buffered job queue
		logger:     cfg.Logger,
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (c *Consumer) Start(ctx context.Context) {
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

func (c *Consumer) Stop() {
	c.connMap.Range(func(_, val any) bool {
		if conn, ok := val.(*websocket.Conn); ok {
			conn.Close()
		}
		return true
	})
	c.wg.Wait()
	close(c.jobQueue)
}

func (c *Consumer) AddSource(ctx context.Context, s Source) {
	// we are already listening to this source
	if _, ok := c.cfg.Sources[s]; ok {
		c.logger.Info("source already present", "source", s)
		return
	}

	c.cfgMu.Lock()
	c.cfg.Sources[s] = struct{}{}
	c.wg.Add(1)
	go c.startConnectionLoop(ctx, s)
	c.cfgMu.Unlock()
}

func (c *Consumer) worker(ctx context.Context) {
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
				c.logger.Error("error deserializing message", "source", j.source.Key(), "err", err)
				return
			}

			// update cursor
			c.cfg.CursorStore.Set(j.source.Key(), time.Now().UnixNano())

			if err := c.cfg.ProcessFunc(ctx, j.source, msg); err != nil {
				c.logger.Error("error processing message", "source", j.source, "err", err)
			}
		}
	}
}

func (c *Consumer) startConnectionLoop(ctx context.Context, source Source) {
	defer c.wg.Done()

	// attempt connection initially
	err := c.runConnection(ctx, source)
	if err != nil {
		c.logger.Error("failed to run connection", "err", err)
	}

	timer := time.NewTimer(1 * time.Minute)
	defer timer.Stop()

	// every subsequent attempt is delayed by 1 minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			err := c.runConnection(ctx, source)
			if err != nil {
				c.logger.Error("failed to run connection", "err", err)
			}
			timer.Reset(1 * time.Minute)
		}
	}
}

func (c *Consumer) runConnection(ctx context.Context, source Source) error {
	cursor := c.cfg.CursorStore.Get(source.Key())

	u, err := source.Url(cursor, c.cfg.Dev)
	if err != nil {
		return err
	}

	c.logger.Info("connecting", "url", u.String())

	retryOpts := []retry.Option{
		retry.Attempts(0), // infinite attempts
		retry.DelayType(retry.BackOffDelay),
		retry.Delay(c.cfg.RetryInterval),
		retry.MaxDelay(c.cfg.MaxRetryInterval),
		retry.MaxJitter(c.cfg.RetryInterval / 5),
		retry.OnRetry(func(n uint, err error) {
			c.logger.Info("retrying connection",
				"source", source,
				"url", u.String(),
				"attempt", n+1,
				"err", err,
			)
		}),
		retry.Context(ctx),
	}

	var conn *websocket.Conn

	err = retry.Do(func() error {
		connCtx, cancel := context.WithTimeout(ctx, c.cfg.ConnectionTimeout)
		defer cancel()
		conn, _, err = c.dialer.DialContext(connCtx, u.String(), nil)
		return err
	}, retryOpts...)
	if err != nil {
		return err
	}

	c.connMap.Store(source, conn)
	defer conn.Close()
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
