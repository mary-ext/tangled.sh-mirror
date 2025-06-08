package state

import (
	"context"
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

	"github.com/posthog/posthog-go"
)

func KnotstreamConsumer(ctx context.Context, c *config.Config, d *db.DB, enforcer *rbac.Enforcer, posthog posthog.Client) (*kc.EventConsumer, error) {
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
		ProcessFunc:       knotstreamIngester(ctx, d, enforcer, posthog, c.Core.Dev),
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

func knotstreamIngester(ctx context.Context, d *db.DB, enforcer *rbac.Enforcer, posthog posthog.Client, dev bool) kc.ProcessFunc {
	return func(ctx context.Context, source kc.EventSource, msg kc.Message) error {
		switch msg.Nsid {
		case tangled.GitRefUpdateNSID:
			return ingestRefUpdate(d, enforcer, posthog, dev, source, msg)
		case tangled.PipelineNSID:
			// TODO
		}

		return nil
	}
}

func ingestRefUpdate(d *db.DB, enforcer *rbac.Enforcer, pc posthog.Client, dev bool, source kc.EventSource, msg kc.Message) error {
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

	knownEmails, err := db.GetAllEmails(d, record.CommitterDid)
	if err != nil {
		return err
	}
	count := 0
	for _, ke := range knownEmails {
		if record.Meta == nil {
			continue
		}
		if record.Meta.CommitCount == nil {
			continue
		}
		for _, ce := range record.Meta.CommitCount.ByEmail {
			if ce == nil {
				continue
			}
			if ce.Email == ke.Address {
				count += int(ce.Count)
			}
		}
	}

	punch := db.Punch{
		Did:   record.CommitterDid,
		Date:  time.Now(),
		Count: count,
	}
	if err := db.AddPunch(d, punch); err != nil {
		return err
	}

	if !dev {
		err = pc.Enqueue(posthog.Capture{
			DistinctId: record.CommitterDid,
			Event:      "git_ref_update",
		})
		if err != nil {
			// non-fatal, TODO: log this
		}
	}

	return nil
}
