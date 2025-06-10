package state

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/cache"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	kc "tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/rbac"
)

func KnotstreamConsumer(c *config.Config, d *db.DB, enforcer *rbac.Enforcer) (*kc.EventConsumer, error) {
	knots, err := db.GetCompletedRegistrations(d)
	if err != nil {
		return nil, err
	}

	srcs := make(map[kc.EventSource]struct{})
	for _, k := range knots {
		s := kc.EventSource{k}
		srcs[s] = struct{}{}
	}

	logger := log.New("knotstream")
	cache := cache.New(c.Redis.Addr)
	cursorStore := kc.NewRedisCursorStore(cache)

	cfg := kc.ConsumerConfig{
		Sources:           srcs,
		ProcessFunc:       knotstreamIngester(d, enforcer),
		RetryInterval:     c.Knotstream.RetryInterval,
		MaxRetryInterval:  c.Knotstream.MaxRetryInterval,
		ConnectionTimeout: c.Knotstream.ConnectionTimeout,
		WorkerCount:       c.Knotstream.WorkerCount,
		QueueSize:         c.Knotstream.QueueSize,
		Logger:            logger,
		Dev:               c.Core.Dev,
		CursorStore:       &cursorStore,
	}

	return kc.NewEventConsumer(cfg), nil
}

func knotstreamIngester(d *db.DB, enforcer *rbac.Enforcer) kc.ProcessFunc {
	return func(source kc.EventSource, msg kc.Message) error {
		switch msg.Nsid {
		case tangled.GitRefUpdateNSID:
			return ingestRefUpdate(d, enforcer, source, msg)
		case tangled.PipelineNSID:
			// TODO
		}

		return nil
	}
}

func ingestRefUpdate(d *db.DB, enforcer *rbac.Enforcer, source kc.EventSource, msg kc.Message) error {
	var record tangled.GitRefUpdate
	err := json.Unmarshal(msg.EventJson, &record)
	if err != nil {
		return err
	}

	knownKnots, err := enforcer.GetDomainsForUser(record.CommitterDid)
	if err != nil {
		return err
	}

	if !slices.Contains(knownKnots, source.Knot) {
		return fmt.Errorf("%s does not belong to %s, something is fishy", record.CommitterDid, source.Knot)
	}

	punch := db.Punch{
		Did:   record.CommitterDid,
		Date:  time.Now(),
		Count: 1,
	}
	if err := db.AddPunch(d, punch); err != nil {
		return err
	}

	return nil
}
