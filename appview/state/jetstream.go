package state

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/bluesky-social/jetstream/pkg/models"
	tangled "github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/appview/db"
)

type Ingester func(ctx context.Context, e *models.Event) error

func jetstreamIngester(db *db.DB) Ingester {
	return func(ctx context.Context, e *models.Event) error {
		var err error
		defer func() {
			eventTime := e.TimeUS
			lastTimeUs := eventTime + 1
			if err := db.UpdateLastTimeUs(lastTimeUs); err != nil {
				err = fmt.Errorf("(deferred) failed to save last time us: %w", err)
			}
		}()

		if e.Kind != models.EventKindCommit {
			return nil
		}

		did := e.Did
		raw := json.RawMessage(e.Commit.Record)

		switch e.Commit.Collection {
		case tangled.GraphFollowNSID:
			record := tangled.GraphFollow{}
			err := json.Unmarshal(raw, &record)
			if err != nil {
				log.Println("invalid record")
				return err
			}
			err = db.AddFollow(did, record.Subject, e.Commit.RKey)
			if err != nil {
				return fmt.Errorf("failed to add follow to db: %w", err)
			}
		}

		return err
	}
}
