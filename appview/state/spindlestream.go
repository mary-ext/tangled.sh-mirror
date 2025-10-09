package state

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/cache"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	ec "tangled.org/core/eventconsumer"
	"tangled.org/core/eventconsumer/cursor"
	"tangled.org/core/log"
	"tangled.org/core/rbac"
	spindle "tangled.org/core/spindle/models"
)

func Spindlestream(ctx context.Context, c *config.Config, d *db.DB, enforcer *rbac.Enforcer) (*ec.Consumer, error) {
	logger := log.FromContext(ctx)
	logger = log.SubLogger(logger, "spindlestream")

	spindles, err := db.GetSpindles(
		d,
		db.FilterIsNot("verified", "null"),
	)
	if err != nil {
		return nil, err
	}

	srcs := make(map[ec.Source]struct{})
	for _, s := range spindles {
		src := ec.NewSpindleSource(s.Instance)
		srcs[src] = struct{}{}
	}

	cache := cache.New(c.Redis.Addr)
	cursorStore := cursor.NewRedisCursorStore(cache)

	cfg := ec.ConsumerConfig{
		Sources:           srcs,
		ProcessFunc:       spindleIngester(ctx, logger, d),
		RetryInterval:     c.Spindlestream.RetryInterval,
		MaxRetryInterval:  c.Spindlestream.MaxRetryInterval,
		ConnectionTimeout: c.Spindlestream.ConnectionTimeout,
		WorkerCount:       c.Spindlestream.WorkerCount,
		QueueSize:         c.Spindlestream.QueueSize,
		Logger:            logger,
		Dev:               c.Core.Dev,
		CursorStore:       &cursorStore,
	}

	return ec.NewConsumer(cfg), nil
}

func spindleIngester(ctx context.Context, logger *slog.Logger, d *db.DB) ec.ProcessFunc {
	return func(ctx context.Context, source ec.Source, msg ec.Message) error {
		switch msg.Nsid {
		case tangled.PipelineStatusNSID:
			return ingestPipelineStatus(ctx, logger, d, source, msg)
		}

		return nil
	}
}

func ingestPipelineStatus(ctx context.Context, logger *slog.Logger, d *db.DB, source ec.Source, msg ec.Message) error {
	var record tangled.PipelineStatus
	err := json.Unmarshal(msg.EventJson, &record)
	if err != nil {
		return err
	}

	pipelineUri, err := syntax.ParseATURI(record.Pipeline)
	if err != nil {
		return err
	}

	exitCode := 0
	if record.ExitCode != nil {
		exitCode = int(*record.ExitCode)
	}

	// pick the record creation time if possible, or use time.Now
	created := time.Now()
	if t, err := time.Parse(time.RFC3339, record.CreatedAt); err == nil && created.After(t) {
		created = t
	}

	status := models.PipelineStatus{
		Spindle:      source.Key(),
		Rkey:         msg.Rkey,
		PipelineKnot: strings.TrimPrefix(pipelineUri.Authority().String(), "did:web:"),
		PipelineRkey: pipelineUri.RecordKey().String(),
		Created:      created,
		Workflow:     record.Workflow,
		Status:       spindle.StatusKind(record.Status),
		Error:        record.Error,
		ExitCode:     exitCode,
	}

	err = db.AddPipelineStatus(d, status)
	if err != nil {
		return fmt.Errorf("failed to add pipeline status: %w", err)
	}

	return nil
}
