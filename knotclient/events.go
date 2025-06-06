package knotclient

import (
	"context"
	"log/slog"
	"math/rand"
	"net/url"
	"sync"
	"time"

	"tangled.sh/tangled.sh/core/log"

	"github.com/gorilla/websocket"
)

type ProcessFunc func(source string, message []byte) error

type ConsumerConfig struct {
	Sources           []string
	ProcessFunc       ProcessFunc
	RetryInterval     time.Duration
	MaxRetryInterval  time.Duration
	ConnectionTimeout time.Duration
	WorkerCount       int
	QueueSize         int
	Logger            *slog.Logger
}

type EventConsumer struct {
	cfg        ConsumerConfig
	wg         sync.WaitGroup
	dialer     *websocket.Dialer
	connMap    sync.Map
	jobQueue   chan job
	logger     *slog.Logger
	randSource *rand.Rand
}

type job struct {
	source  string
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
	return &EventConsumer{
		cfg:        cfg,
		dialer:     websocket.DefaultDialer,
		jobQueue:   make(chan job, cfg.QueueSize), // buffered job queue
		logger:     cfg.Logger,
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (c *EventConsumer) Start(ctx context.Context) {
	// start workers
	for range c.cfg.WorkerCount {
		c.wg.Add(1)
		go c.worker(ctx)
	}

	// start streaming
	for _, source := range c.cfg.Sources {
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
			if err := c.cfg.ProcessFunc(j.source, j.message); err != nil {
				c.logger.Error("error processing message", "source", j.source, "err", err)
			}
		}
	}
}

func (c *EventConsumer) startConnectionLoop(ctx context.Context, source string) {
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

func (c *EventConsumer) runConnection(ctx context.Context, source string) error {
	connCtx, cancel := context.WithTimeout(ctx, c.cfg.ConnectionTimeout)
	defer cancel()

	u, err := url.Parse(source)
	if err != nil {
		return err
	}

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
