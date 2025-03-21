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

type JetstreamSubscriber struct {
	client  *client.Client
	cancel  context.CancelFunc
	dids    []string
	ident   string
	running bool
}

type JetstreamClient struct {
	cfg                  *client.ClientConfig
	baseIdent            string
	l                    *slog.Logger
	db                   DB
	waitForDid           bool
	maxDidsPerSubscriber int

	mu           sync.RWMutex
	subscribers  []*JetstreamSubscriber
	processFunc  func(context.Context, *models.Event) error
	subscriberWg sync.WaitGroup
}

func (j *JetstreamClient) AddDid(did string) {
	if did == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	// Just add to the config for now, actual subscriber management happens in UpdateDids
	j.cfg.WantedDids = append(j.cfg.WantedDids, did)
}

func (j *JetstreamClient) UpdateDids(dids []string) {
	j.mu.Lock()
	for _, did := range dids {
		if did != "" {
			j.cfg.WantedDids = append(j.cfg.WantedDids, did)
		}
	}

	needRebalance := j.processFunc != nil
	j.mu.Unlock()

	if needRebalance {
		j.rebalanceSubscribers()
	}
}

func NewJetstreamClient(endpoint, ident string, collections []string, cfg *client.ClientConfig, logger *slog.Logger, db DB, waitForDid bool) (*JetstreamClient, error) {
	if cfg == nil {
		cfg = client.DefaultClientConfig()
		cfg.WebsocketURL = endpoint
		cfg.WantedCollections = collections
	}

	return &JetstreamClient{
		cfg:                  cfg,
		baseIdent:            ident,
		db:                   db,
		l:                    logger,
		waitForDid:           waitForDid,
		subscribers:          make([]*JetstreamSubscriber, 0),
		maxDidsPerSubscriber: 100,
	}, nil
}

// StartJetstream starts the jetstream client and processes events using the provided processFunc.
// The caller is responsible for saving the last time_us to the database (just use your db.SaveLastTimeUs).
func (j *JetstreamClient) StartJetstream(ctx context.Context, processFunc func(context.Context, *models.Event) error) error {
	j.mu.Lock()
	j.processFunc = processFunc
	j.mu.Unlock()

	if j.waitForDid {
		// Start a goroutine to wait for DIDs and then start subscribers
		go func() {
			for {
				j.mu.RLock()
				hasDids := len(j.cfg.WantedDids) > 0
				j.mu.RUnlock()

				if hasDids {
					j.l.Info("done waiting for did, starting subscribers")
					j.rebalanceSubscribers()
					return
				}
				time.Sleep(time.Second)
			}
		}()
	} else {
		// Start subscribers immediately
		j.rebalanceSubscribers()
	}

	return nil
}

// rebalanceSubscribers creates, updates, or removes subscribers based on the current list of DIDs
func (j *JetstreamClient) rebalanceSubscribers() {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.processFunc == nil {
		j.l.Warn("cannot rebalance subscribers without a process function")
		return
	}

	// calculate how many subscribers we need
	totalDids := len(j.cfg.WantedDids)
	subscribersNeeded := (totalDids + j.maxDidsPerSubscriber - 1) / j.maxDidsPerSubscriber // ceiling division

	// first case: no subscribers yet; create all needed subscribers
	if len(j.subscribers) == 0 {
		for i := range subscribersNeeded {
			startIdx := i * j.maxDidsPerSubscriber
			endIdx := min((i+1)*j.maxDidsPerSubscriber, totalDids)

			subscriberDids := j.cfg.WantedDids[startIdx:endIdx]

			subCfg := *j.cfg
			subCfg.WantedDids = subscriberDids

			ident := fmt.Sprintf("%s-%d", j.baseIdent, i)
			subscriber := &JetstreamSubscriber{
				dids:  subscriberDids,
				ident: ident,
			}
			j.subscribers = append(j.subscribers, subscriber)

			j.subscriberWg.Add(1)
			go j.startSubscriber(subscriber, &subCfg)
		}
		return
	}

	// second case: we have more subscribers than needed, stop extra subscribers
	if len(j.subscribers) > subscribersNeeded {
		for i := subscribersNeeded; i < len(j.subscribers); i++ {
			sub := j.subscribers[i]
			if sub.running && sub.cancel != nil {
				sub.cancel()
				sub.running = false
			}
		}
		j.subscribers = j.subscribers[:subscribersNeeded]
	}

	// third case: we need more subscribers
	if len(j.subscribers) < subscribersNeeded {
		existingCount := len(j.subscribers)
		// Create additional subscribers
		for i := existingCount; i < subscribersNeeded; i++ {
			startIdx := i * j.maxDidsPerSubscriber
			endIdx := min((i+1)*j.maxDidsPerSubscriber, totalDids)

			subscriberDids := j.cfg.WantedDids[startIdx:endIdx]

			subCfg := *j.cfg
			subCfg.WantedDids = subscriberDids

			ident := fmt.Sprintf("%s-%d", j.baseIdent, i)
			subscriber := &JetstreamSubscriber{
				dids:  subscriberDids,
				ident: ident,
			}
			j.subscribers = append(j.subscribers, subscriber)

			j.subscriberWg.Add(1)
			go j.startSubscriber(subscriber, &subCfg)
		}
	}

	// fourth case: update existing subscribers with new wantedDids
	for i := 0; i < subscribersNeeded && i < len(j.subscribers); i++ {
		startIdx := i * j.maxDidsPerSubscriber
		endIdx := min((i+1)*j.maxDidsPerSubscriber, totalDids)
		newDids := j.cfg.WantedDids[startIdx:endIdx]

		// if the dids for this subscriber have changed, restart it
		sub := j.subscribers[i]
		if !didSlicesEqual(sub.dids, newDids) {
			j.l.Info("subscriber DIDs changed, updating",
				"subscriber", sub.ident,
				"old_count", len(sub.dids),
				"new_count", len(newDids))

			if sub.running && sub.cancel != nil {
				sub.cancel()
				sub.running = false
			}

			subCfg := *j.cfg
			subCfg.WantedDids = newDids

			sub.dids = newDids

			j.subscriberWg.Add(1)
			go j.startSubscriber(sub, &subCfg)
		}
	}
}

func didSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	aMap := make(map[string]struct{}, len(a))
	for _, did := range a {
		aMap[did] = struct{}{}
	}

	for _, did := range b {
		if _, exists := aMap[did]; !exists {
			return false
		}
	}

	return true
}

// startSubscriber initializes and starts a single subscriber
func (j *JetstreamClient) startSubscriber(sub *JetstreamSubscriber, cfg *client.ClientConfig) {
	defer j.subscriberWg.Done()

	logger := j.l.With("subscriber", sub.ident)
	logger.Info("starting subscriber", "dids_count", len(sub.dids))

	sched := sequential.NewScheduler(sub.ident, logger, j.processFunc)

	client, err := client.NewClient(cfg, log.New("jetstream-"+sub.ident), sched)
	if err != nil {
		logger.Error("failed to create jetstream client", "error", err)
		return
	}

	sub.client = client

	j.mu.Lock()
	sub.running = true
	j.mu.Unlock()

	j.connectAndReadForSubscriber(sub)
}

func (j *JetstreamClient) connectAndReadForSubscriber(sub *JetstreamSubscriber) {
	ctx := context.Background()
	l := j.l.With("subscriber", sub.ident)

	for {
		// Check if this subscriber should still be running
		j.mu.RLock()
		running := sub.running
		j.mu.RUnlock()

		if !running {
			l.Info("subscriber marked for shutdown")
			return
		}

		cursor := j.getLastTimeUs(ctx)

		connCtx, cancel := context.WithCancel(ctx)

		j.mu.Lock()
		sub.cancel = cancel
		j.mu.Unlock()

		l.Info("connecting subscriber to jetstream")
		if err := sub.client.ConnectAndRead(connCtx, cursor); err != nil {
			l.Error("error reading jetstream", "error", err)
			cancel()
			time.Sleep(time.Second) // Small backoff before retry
			continue
		}

		select {
		case <-ctx.Done():
			l.Info("context done, stopping subscriber")
			return
		case <-connCtx.Done():
			l.Info("connection context done, reconnecting")
			continue
		}
	}
}

// GetRunningSubscribersCount returns the total number of currently running subscribers
func (j *JetstreamClient) GetRunningSubscribersCount() int {
	j.mu.RLock()
	defer j.mu.RUnlock()

	runningCount := 0
	for _, sub := range j.subscribers {
		if sub.running {
			runningCount++
		}
	}

	return runningCount
}

// Shutdown gracefully stops all subscribers
func (j *JetstreamClient) Shutdown() {
	j.mu.Lock()

	// Cancel all subscribers
	for _, sub := range j.subscribers {
		if sub.running && sub.cancel != nil {
			sub.cancel()
			sub.running = false
		}
	}

	j.mu.Unlock()

	// Wait for all subscribers to complete
	j.subscriberWg.Wait()
	j.l.Info("all subscribers shut down", "total_subscribers", len(j.subscribers), "running_subscribers", j.GetRunningSubscribersCount())
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
		err = j.db.UpdateLastTimeUs(lastTimeUs)
		if err != nil {
			l.Error("failed to save last time us", "error", err)
		}
	}

	l.Info("found last time_us", "time_us", lastTimeUs, "running_subscribers", j.GetRunningSubscribersCount())
	return &lastTimeUs
}
