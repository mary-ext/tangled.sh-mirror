package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/knotserver/jsclient"
)

func (h *Handle) StartJetstream(ctx context.Context) error {
	collections := []string{tangled.PublicKeyNSID, tangled.KnotMemberNSID}
	dids := []string{}

	lastTimeUs, err := h.getLastTimeUs()
	if err != nil {
		return err
	}

	h.js = jsclient.NewJetstreamClient(collections, dids)
	messages, err := h.js.ReadJetstream(ctx, lastTimeUs)
	if err != nil {
		return fmt.Errorf("failed to read from jetstream: %w", err)
	}

	go h.processMessages(messages)

	return nil
}

func (h *Handle) getLastTimeUs() (int64, error) {
	lastTimeUs, err := h.db.GetLastTimeUs()
	if err != nil {
		log.Println("couldn't get last time us, starting from now")
		lastTimeUs = time.Now().UnixMicro()
	}

	// If last time is older than a week, start from now
	if time.Now().UnixMicro()-lastTimeUs > 7*24*60*60*1000*1000 {
		lastTimeUs = time.Now().UnixMicro()
		log.Printf("last time us is older than a week. discarding that and starting from now.")
		err = h.db.SaveLastTimeUs(lastTimeUs)
		if err != nil {
			log.Println("failed to save last time us")
		}
	}

	log.Printf("found last time_us %d", lastTimeUs)
	return lastTimeUs, nil
}

func (h *Handle) processPublicKey(did string, record map[string]interface{}) {
	if err := h.db.AddPublicKeyFromRecord(did, record); err != nil {
		log.Printf("failed to add public key: %v", err)
	} else {
		log.Printf("added public key from firehose: %s", did)
	}
}

func (h *Handle) fetchAndAddKeys(did string) {
	resp, err := http.Get(path.Join(h.c.AppViewEndpoint, did))
	if err != nil {
		log.Printf("error getting keys for %s: %v", did, err)
		return
	}
	defer resp.Body.Close()

	plaintext, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body: %v", err)
		return
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
			log.Printf("failed to add public key: %v", err)
		}
	}
}

func (h *Handle) processKnotMember(did string, record map[string]interface{}) {
	ok, err := h.e.E.Enforce(did, ThisServer, ThisServer, "server:invite")
	if err != nil || !ok {
		log.Printf("failed to add member from did %s", did)
		return
	}

	log.Printf("adding member")
	if err := h.e.AddMember(ThisServer, record["member"].(string)); err != nil {
		log.Printf("failed to add member: %v", err)
	} else {
		log.Printf("added member from firehose: %s", record["member"])
	}

	h.fetchAndAddKeys(did)
	h.js.UpdateDids([]string{did})
}

func (h *Handle) processMessages(messages <-chan []byte) {
	log.Println("waiting for knot to be initialized")
	<-h.init
	log.Println("initalized jetstream watcher")

	for msg := range messages {
		var data map[string]interface{}
		if err := json.Unmarshal(msg, &data); err != nil {
			log.Printf("error unmarshaling message: %v", err)
			continue
		}

		if kind, ok := data["kind"].(string); ok && kind == "commit" {
			commit := data["commit"].(map[string]interface{})
			did := data["did"].(string)
			record := commit["record"].(map[string]interface{})

			switch commit["collection"].(string) {
			case tangled.PublicKeyNSID:
				h.processPublicKey(did, record)
			case tangled.KnotMemberNSID:
				h.processKnotMember(did, record)
			}

			lastTimeUs := int64(data["time_us"].(float64))
			if err := h.db.SaveLastTimeUs(lastTimeUs); err != nil {
				log.Printf("failed to save last time us: %v", err)
			}
		}
	}
}
