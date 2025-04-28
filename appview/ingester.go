package appview

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/jetstream/pkg/models"
	tangled "tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/db"
)

type Ingester func(ctx context.Context, e *models.Event) error

func Ingest(d db.DbWrapper) Ingester {
	return func(ctx context.Context, e *models.Event) error {
		var err error
		defer func() {
			eventTime := e.TimeUS
			lastTimeUs := eventTime + 1
			if err := d.SaveLastTimeUs(lastTimeUs); err != nil {
				err = fmt.Errorf("(deferred) failed to save last time us: %w", err)
			}
		}()

		if e.Kind != models.EventKindCommit {
			return nil
		}

		switch e.Commit.Collection {
		case tangled.GraphFollowNSID:
			ingestFollow(&d, e)
		case tangled.FeedStarNSID:
			ingestStar(&d, e)
		case tangled.PublicKeyNSID:
			ingestPublicKey(&d, e)
		}

		return err
	}
}

func ingestStar(d *db.DbWrapper, e *models.Event) error {
	var err error
	did := e.Did

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		var subjectUri syntax.ATURI

		raw := json.RawMessage(e.Commit.Record)
		record := tangled.FeedStar{}
		err := json.Unmarshal(raw, &record)
		if err != nil {
			log.Println("invalid record")
			return err
		}

		subjectUri, err = syntax.ParseATURI(record.Subject)
		if err != nil {
			log.Println("invalid record")
			return err
		}
		err = db.AddStar(d, did, subjectUri, e.Commit.RKey)
	case models.CommitOperationDelete:
		err = db.DeleteStarByRkey(d, did, e.Commit.RKey)
	}

	if err != nil {
		return fmt.Errorf("failed to %s star record: %w", e.Commit.Operation, err)
	}

	return nil
}

func ingestFollow(d *db.DbWrapper, e *models.Event) error {
	var err error
	did := e.Did

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.GraphFollow{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			log.Println("invalid record")
			return err
		}

		subjectDid := record.Subject
		err = db.AddFollow(d, did, subjectDid, e.Commit.RKey)
	case models.CommitOperationDelete:
		err = db.DeleteFollowByRkey(d, did, e.Commit.RKey)
	}

	if err != nil {
		return fmt.Errorf("failed to %s follow record: %w", e.Commit.Operation, err)
	}

	return nil
}

func ingestPublicKey(d *db.DbWrapper, e *models.Event) error {
	did := e.Did
	var err error

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		log.Println("processing add of pubkey")
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.PublicKey{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			log.Printf("invalid record: %s", err)
			return err
		}

		name := record.Name
		key := record.Key
		err = db.AddPublicKey(d, did, name, key, e.Commit.RKey)
	case models.CommitOperationDelete:
		log.Println("processing delete of pubkey")
		err = db.DeletePublicKeyByRkey(d, did, e.Commit.RKey)
	}

	if err != nil {
		return fmt.Errorf("failed to %s pubkey record: %w", e.Commit.Operation, err)
	}

	return nil
}
