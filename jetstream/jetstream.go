package jetstream

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bluesky-social/jetstream/pkg/client"
	"github.com/bluesky-social/jetstream/pkg/client/schedulers/sequential"
	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/sotangled/tangled/log"
)

type DB interface {
	GetLastTimeUs() (int64, error)
	SaveLastTimeUs(int64) error
	UpdateLastTimeUs(int64) error
}

type JetstreamClient struct {
	cfg    *client.ClientConfig
	client *client.Client
	ident  string
	l      *slog.Logger

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
	j.cfg.WantedDids = append(j.cfg.WantedDids, did)
	j.mu.Unlock()
}

func (j *JetstreamClient) UpdateDids(dids []string) {
	j.mu.Lock()
	for _, did := range dids {
		if did != "" {
			j.cfg.WantedDids = append(j.cfg.WantedDids, did)
		}
	}
	j.mu.Unlock()

	j.cancelMu.Lock()
	if j.cancel != nil {
		j.cancel()
	}
	j.cancelMu.Unlock()
}

func NewJetstreamClient(ident string, collections []string, cfg *client.ClientConfig, logger *slog.Logger, db DB, waitForDid bool) (*JetstreamClient, error) {
	if cfg == nil {
		cfg = client.DefaultClientConfig()
		cfg.WebsocketURL = "wss://jetstream1.us-west.bsky.network/subscribe"
		cfg.WantedCollections = collections
	}

	return &JetstreamClient{
		cfg:   cfg,
		ident: ident,
		db:    db,
		l:     logger,

		// This will make the goroutine in StartJetstream wait until
		// cfg.WantedDids has been populated, typically using UpdateDids.
		waitForDid: waitForDid,
	}, nil
}

// StartJetstream starts the jetstream client and processes events using the provided processFunc.
// The caller is responsible for saving the last time_us to the database (just use your db.SaveLastTimeUs).
func (j *JetstreamClient) StartJetstream(ctx context.Context, processFunc func(context.Context, *models.Event) error) error {
	logger := j.l

	sched := sequential.NewScheduler(j.ident, logger, processFunc)

	client, err := client.NewClient(j.cfg, log.New("jetstream"), sched)
	if err != nil {
		return fmt.Errorf("failed to create jetstream client: %w", err)
	}
	j.client = client

	go func() {
		if j.waitForDid {
			for len(j.cfg.WantedDids) == 0 {
				time.Sleep(time.Second)
			}
		}
		logger.Info("done waiting for did")
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

	// If last time is older than a week, start from now
	if time.Now().UnixMicro()-lastTimeUs > 2*24*60*60*1000*1000 {
		lastTimeUs = time.Now().UnixMicro()
		l.Warn("last time us is older than 2 days; discarding that and starting from now")
		err = j.db.UpdateLastTimeUs(lastTimeUs)
		if err != nil {
			l.Error("failed to save last time us", "error", err)
		}
	}

	l.Info("found last time_us", "time_us", lastTimeUs)
	return &lastTimeUs
}
