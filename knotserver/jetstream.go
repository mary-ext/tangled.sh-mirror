package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/log"
)

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
	if event.Kind != models.EventKindCommit {
		return nil
	}

	var err error
	defer func() {
		eventTime := event.TimeUS
		lastTimeUs := eventTime + 1
		fmt.Println("lastTimeUs", lastTimeUs)
		if err := h.db.UpdateLastTimeUs(lastTimeUs); err != nil {
			err = fmt.Errorf("(deferred) failed to save last time us: %w", err)
		}
		h.jc.UpdateDids([]string{did})
	}()

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

	return err
}
