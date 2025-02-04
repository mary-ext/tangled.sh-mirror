package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/knotserver/jsclient"
	"github.com/sotangled/tangled/log"
)

func (h *Handle) StartJetstream(ctx context.Context) error {
	l := h.l.With("component", "jetstream")
	ctx = log.IntoContext(ctx, l)
	collections := []string{tangled.PublicKeyNSID, tangled.KnotMemberNSID}
	dids := []string{}

	lastTimeUs, err := h.getLastTimeUs(ctx)
	if err != nil {
		return err
	}

	h.js = jsclient.NewJetstreamClient(collections, dids)
	messages, err := h.js.ReadJetstream(ctx, lastTimeUs)
	if err != nil {
		return fmt.Errorf("failed to read from jetstream: %w", err)
	}

	go h.processMessages(ctx, messages)

	return nil
}

func (h *Handle) getLastTimeUs(ctx context.Context) (int64, error) {
	l := log.FromContext(ctx)
	lastTimeUs, err := h.db.GetLastTimeUs()
	if err != nil {
		l.Info("couldn't get last time us, starting from now")
		lastTimeUs = time.Now().UnixMicro()
	}

	// If last time is older than a week, start from now
	if time.Now().UnixMicro()-lastTimeUs > 7*24*60*60*1000*1000 {
		lastTimeUs = time.Now().UnixMicro()
		l.Info("last time us is older than a week. discarding that and starting from now")
		err = h.db.SaveLastTimeUs(lastTimeUs)
		if err != nil {
			l.Error("failed to save last time us")
		}
	}

	l.Info("found last time_us", "time_us", lastTimeUs)
	return lastTimeUs, nil
}

func (h *Handle) processPublicKey(ctx context.Context, did string, record map[string]interface{}) error {
	l := log.FromContext(ctx)
	if err := h.db.AddPublicKeyFromRecord(did, record); err != nil {
		l.Error("failed to add public key", "error", err)
		return fmt.Errorf("failed to add public key: %w", err)
	}
	l.Info("added public key from firehose", "did", did)
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

func (h *Handle) processKnotMember(ctx context.Context, did string, record map[string]interface{}) error {
	l := log.FromContext(ctx)
	ok, err := h.e.E.Enforce(did, ThisServer, ThisServer, "server:invite")
	if err != nil || !ok {
		l.Error("failed to add member", "did", did)
		return fmt.Errorf("failed to enforce permissions: %w", err)
	}

	l.Info("adding member")
	if err := h.e.AddMember(ThisServer, record["member"].(string)); err != nil {
		l.Error("failed to add member", "error", err)
		return fmt.Errorf("failed to add member: %w", err)
	}
	l.Info("added member from firehose", "member", record["member"])

	if err := h.db.AddDid(did); err != nil {
		l.Error("failed to add did", "error", err)
		return fmt.Errorf("failed to add did: %w", err)
	}

	if err := h.fetchAndAddKeys(ctx, did); err != nil {
		return fmt.Errorf("failed to fetch and add keys: %w", err)
	}

	h.js.UpdateDids([]string{did})
	return nil
}

func (h *Handle) processMessages(ctx context.Context, messages <-chan []byte) {
	l := log.FromContext(ctx)
	l.Info("waiting for knot to be initialized")
	<-h.init
	l.Info("initialized jetstream watcher")

	for msg := range messages {
		var data map[string]interface{}
		if err := json.Unmarshal(msg, &data); err != nil {
			l.Error("error unmarshaling message", "error", err)
			continue
		}

		if kind, ok := data["kind"].(string); ok && kind == "commit" {
			commit := data["commit"].(map[string]interface{})
			did := data["did"].(string)
			record := commit["record"].(map[string]interface{})

			var processErr error
			switch commit["collection"].(string) {
			case tangled.PublicKeyNSID:
				if err := h.processPublicKey(ctx, did, record); err != nil {
					processErr = fmt.Errorf("failed to process public key: %w", err)
				}
			case tangled.KnotMemberNSID:
				if err := h.processKnotMember(ctx, did, record); err != nil {
					processErr = fmt.Errorf("failed to process knot member: %w", err)
				}
			}

			if processErr != nil {
				l.Error("error processing message", "error", processErr)
				continue
			}

			lastTimeUs := int64(data["time_us"].(float64))
			if err := h.db.SaveLastTimeUs(lastTimeUs); err != nil {
				l.Error("failed to save last time us", "error", err)
				continue
			}
		}
	}
}
