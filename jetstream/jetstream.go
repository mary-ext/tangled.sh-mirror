package jetstream

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/bluesky-social/jetstream/pkg/client"
	"github.com/bluesky-social/jetstream/pkg/client/schedulers/sequential"
	"github.com/bluesky-social/jetstream/pkg/models"
	"tangled.sh/tangled.sh/core/log"
)

type DB interface {
	GetLastTimeUs() (int64, error)
	SaveLastTimeUs(int64) error
}

type Set[T comparable] map[T]struct{}

type JetstreamClient struct {
	cfg    *client.ClientConfig
	client *client.Client
	ident  string
	l      *slog.Logger

	wantedDids Set[string]
	db         DB
	waitForDid bool
	mu         sync.RWMutex

	cancel   context.CancelFunc
	cancelMu sync.Mutex
}

func (j *JetstreamClient) AddDid(did string) {
	if did == "" {
		return
	}

	j.mu.Lock()
	j.wantedDids[did] = struct{}{}
	j.mu.Unlock()
}

type processor func(context.Context, *models.Event) error

func (j *JetstreamClient) withDidFilter(processFunc processor) processor {
	// since this closure references j.WantedDids; it should auto-update
	// existing instances of the closure when j.WantedDids is mutated
	return func(ctx context.Context, evt *models.Event) error {
		if _, ok := j.wantedDids[evt.Did]; ok {
			return processFunc(ctx, evt)
		} else {
			return nil
		}
	}
}

func NewJetstreamClient(endpoint, ident string, collections []string, cfg *client.ClientConfig, logger *slog.Logger, db DB, waitForDid bool) (*JetstreamClient, error) {
	if cfg == nil {
		cfg = client.DefaultClientConfig()
		cfg.WebsocketURL = endpoint
		cfg.WantedCollections = collections
	}

	return &JetstreamClient{
		cfg:        cfg,
		ident:      ident,
		db:         db,
		l:          logger,
		wantedDids: make(map[string]struct{}),

		// This will make the goroutine in StartJetstream wait until
		// j.wantedDids has been populated, typically using addDids.
		waitForDid: waitForDid,
	}, nil
}

// StartJetstream starts the jetstream client and processes events using the provided processFunc.
// The caller is responsible for saving the last time_us to the database (just use your db.UpdateLastTimeUs).
func (j *JetstreamClient) StartJetstream(ctx context.Context, processFunc func(context.Context, *models.Event) error) error {
	logger := j.l

	sched := sequential.NewScheduler(j.ident, logger, j.withDidFilter(processFunc))

	client, err := client.NewClient(j.cfg, log.New("jetstream"), sched)
	if err != nil {
		return fmt.Errorf("failed to create jetstream client: %w", err)
	}
	j.client = client

	go func() {
		if j.waitForDid {
			for len(j.wantedDids) == 0 {
				time.Sleep(time.Second)
			}
		}
		logger.Info("done waiting for did")

		go j.periodicLastTimeSave(ctx)
		j.saveIfKilled(ctx)

		j.connectAndRead(ctx)
	}()

	return nil
}

func (j *JetstreamClient) connectAndRead(ctx context.Context) {
	l := log.FromContext(ctx)
	for {
		cursor := j.getLastTimeUs(ctx)

		connCtx, cancel := context.WithCancel(ctx)
		j.cancelMu.Lock()
		j.cancel = cancel
		j.cancelMu.Unlock()

		if err := j.client.ConnectAndRead(connCtx, cursor); err != nil {
			l.Error("error reading jetstream", "error", err)
			cancel()
			continue
		}

		select {
		case <-ctx.Done():
			l.Info("context done, stopping jetstream")
			return
		case <-connCtx.Done():
			l.Info("connection context done, reconnecting")
			continue
		}
	}
}

// save cursor periodically
func (j *JetstreamClient) periodicLastTimeSave(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.db.SaveLastTimeUs(time.Now().UnixMicro())
		}
	}
}

func (j *JetstreamClient) getLastTimeUs(ctx context.Context) *int64 {
	l := log.FromContext(ctx)
	lastTimeUs, err := j.db.GetLastTimeUs()
	if err != nil {
		l.Warn("couldn't get last time us, starting from now", "error", err)
		lastTimeUs = time.Now().UnixMicro()
		err = j.db.SaveLastTimeUs(lastTimeUs)
		if err != nil {
			l.Error("failed to save last time us", "error", err)
		}
	}

	// If last time is older than 2 days, start from now
	if time.Now().UnixMicro()-lastTimeUs > 2*24*60*60*1000*1000 {
		lastTimeUs = time.Now().UnixMicro()
		l.Warn("last time us is older than 2 days; discarding that and starting from now")
		err = j.db.SaveLastTimeUs(lastTimeUs)
		if err != nil {
			l.Error("failed to save last time us", "error", err)
		}
	}

	l.Info("found last time_us", "time_us", lastTimeUs)
	return &lastTimeUs
}

func (j *JetstreamClient) saveIfKilled(ctx context.Context) context.Context {
	ctxWithCancel, cancel := context.WithCancel(ctx)

	sigChan := make(chan os.Signal, 1)

	signal.Notify(sigChan,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGHUP,
		syscall.SIGKILL,
		syscall.SIGSTOP,
	)

	go func() {
		sig := <-sigChan
		j.l.Info("Received signal, initiating graceful shutdown", "signal", sig)

		lastTimeUs := time.Now().UnixMicro()
		if err := j.db.SaveLastTimeUs(lastTimeUs); err != nil {
			j.l.Error("Failed to save last time during shutdown", "error", err)
		}
		j.l.Info("Saved lastTimeUs before shutdown", "lastTimeUs", lastTimeUs)

		j.cancelMu.Lock()
		if j.cancel != nil {
			j.cancel()
		}
		j.cancelMu.Unlock()

		cancel()

		os.Exit(0)
	}()

	return ctxWithCancel
}
