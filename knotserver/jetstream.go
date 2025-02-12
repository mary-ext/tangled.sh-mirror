package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bluesky-social/jetstream/pkg/client"
	"github.com/bluesky-social/jetstream/pkg/client/schedulers/sequential"
	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/log"
)

type JetstreamClient struct {
	cfg         *client.ClientConfig
	client      *client.Client
	reconnectCh chan struct{}
	mu          sync.RWMutex
}

func (h *Handle) StartJetstream(ctx context.Context) error {
	l := h.l
	ctx = log.IntoContext(ctx, l)
	collections := []string{tangled.PublicKeyNSID, tangled.KnotMemberNSID}
	dids := []string{}

	cfg := client.DefaultClientConfig()
	cfg.WebsocketURL = "wss://jetstream1.us-west.bsky.network/subscribe"
	cfg.WantedCollections = collections
	cfg.WantedDids = dids

	sched := sequential.NewScheduler("knotserver", l, h.processMessages)

	client, err := client.NewClient(cfg, l, sched)
	if err != nil {
		l.Error("failed to create jetstream client", "error", err)
	}

	jc := &JetstreamClient{
		cfg:         cfg,
		client:      client,
		reconnectCh: make(chan struct{}, 1),
	}

	h.jc = jc

	go func() {
		lastTimeUs := h.getLastTimeUs(ctx)
		for len(h.jc.cfg.WantedDids) == 0 {
			time.Sleep(time.Second)
		}
		h.connectAndRead(ctx, &lastTimeUs)
	}()
	return nil
}

func (h *Handle) connectAndRead(ctx context.Context, cursor *int64) {
	l := log.FromContext(ctx)
	for {
		select {
		case <-h.jc.reconnectCh:
			l.Info("(re)connecting jetstream client")
			h.jc.client.Scheduler.Shutdown()
			if err := h.jc.client.ConnectAndRead(ctx, cursor); err != nil {
				l.Error("error reading jetstream", "error", err)
			}
		default:
			if err := h.jc.client.ConnectAndRead(ctx, cursor); err != nil {
				l.Error("error reading jetstream", "error", err)
			}
		}
	}
}

func (j *JetstreamClient) AddDid(did string) {
	j.mu.Lock()
	j.cfg.WantedDids = append(j.cfg.WantedDids, did)
	j.mu.Unlock()
	j.reconnectCh <- struct{}{}
}

func (j *JetstreamClient) UpdateDids(dids []string) {
	j.mu.Lock()
	j.cfg.WantedDids = dids
	j.mu.Unlock()
	j.reconnectCh <- struct{}{}
}

func (h *Handle) getLastTimeUs(ctx context.Context) int64 {
	l := log.FromContext(ctx)
	lastTimeUs, err := h.db.GetLastTimeUs()
	if err != nil {
		l.Warn("couldn't get last time us, starting from now", "error", err)
		lastTimeUs = time.Now().UnixMicro()
		err = h.db.SaveLastTimeUs(lastTimeUs)
		if err != nil {
			l.Error("failed to save last time us")
		}
	}

	// If last time is older than a week, start from now
	if time.Now().UnixMicro()-lastTimeUs > 7*24*60*60*1000*1000 {
		lastTimeUs = time.Now().UnixMicro()
		l.Warn("last time us is older than a week. discarding that and starting from now")
		err = h.db.SaveLastTimeUs(lastTimeUs)
		if err != nil {
			l.Error("failed to save last time us")
		}
	}

	l.Info("found last time_us", "time_us", lastTimeUs)
	return lastTimeUs
}

func (h *Handle) processPublicKey(ctx context.Context, did string, record tangled.PublicKey) error {
	l := log.FromContext(ctx)
	pk := db.PublicKey{
		Did:       did,
		PublicKey: record,
	}
	if err := h.db.AddPublicKey(pk); err != nil {
		l.Error("failed to add public key", "error", err)
		return fmt.Errorf("failed to add public key: %w", err)
	}
	l.Info("added public key from firehose", "did", did)
	return nil
}

func (h *Handle) processKnotMember(ctx context.Context, did string, record tangled.KnotMember) error {
	l := log.FromContext(ctx)

	if record.Domain != h.c.Server.Hostname {
		l.Error("domain mismatch", "domain", record.Domain, "expected", h.c.Server.Hostname)
		return fmt.Errorf("domain mismatch: %s != %s", record.Domain, h.c.Server.Hostname)
	}

	ok, err := h.e.E.Enforce(did, ThisServer, ThisServer, "server:invite")
	if err != nil || !ok {
		l.Error("failed to add member", "did", did)
		return fmt.Errorf("failed to enforce permissions: %w", err)
	}

	l.Info("adding member")
	if err := h.e.AddMember(ThisServer, record.Member); err != nil {
		l.Error("failed to add member", "error", err)
		return fmt.Errorf("failed to add member: %w", err)
	}
	l.Info("added member from firehose", "member", record.Member)

	if err := h.db.AddDid(did); err != nil {
		l.Error("failed to add did", "error", err)
		return fmt.Errorf("failed to add did: %w", err)
	}

	if err := h.fetchAndAddKeys(ctx, did); err != nil {
		return fmt.Errorf("failed to fetch and add keys: %w", err)
	}

	h.jc.UpdateDids([]string{did})
	return nil
}

func (h *Handle) fetchAndAddKeys(ctx context.Context, did string) error {
	l := log.FromContext(ctx)

	keysEndpoint, err := url.JoinPath(h.c.AppViewEndpoint, "keys", did)
	if err != nil {
		l.Error("error building endpoint url", "did", did, "error", err.Error())
		return fmt.Errorf("error building endpoint url: %w", err)
	}

	resp, err := http.Get(keysEndpoint)
	if err != nil {
		l.Error("error getting keys", "did", did, "error", err)
		return fmt.Errorf("error getting keys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		l.Info("no keys found for did", "did", did)
		return nil
	}

	plaintext, err := io.ReadAll(resp.Body)
	if err != nil {
		l.Error("error reading response body", "error", err)
		return fmt.Errorf("error reading response body: %w", err)
	}

	for _, key := range strings.Split(string(plaintext), "\n") {
		if key == "" {
			continue
		}
		pk := db.PublicKey{
			Did: did,
		}
		pk.Key = key
		if err := h.db.AddPublicKey(pk); err != nil {
			l.Error("failed to add public key", "error", err)
			return fmt.Errorf("failed to add public key: %w", err)
		}
	}
	return nil
}

func (h *Handle) processMessages(ctx context.Context, event *models.Event) error {
	did := event.Did

	raw := json.RawMessage(event.Commit.Record)

	switch event.Commit.Collection {
	case tangled.PublicKeyNSID:
		var record tangled.PublicKey
		if err := json.Unmarshal(raw, &record); err != nil {
			return fmt.Errorf("failed to unmarshal record: %w", err)
		}
		if err := h.processPublicKey(ctx, did, record); err != nil {
			return fmt.Errorf("failed to process public key: %w", err)
		}

	case tangled.KnotMemberNSID:
		var record tangled.KnotMember
		if err := json.Unmarshal(raw, &record); err != nil {
			return fmt.Errorf("failed to unmarshal record: %w", err)
		}
		if err := h.processKnotMember(ctx, did, record); err != nil {
			return fmt.Errorf("failed to process knot member: %w", err)
		}
	}

	lastTimeUs := event.TimeUS
	if err := h.db.SaveLastTimeUs(lastTimeUs); err != nil {
		return fmt.Errorf("failed to save last time us: %w", err)
	}

	return nil
}
