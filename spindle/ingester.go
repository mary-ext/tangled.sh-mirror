package spindle

import (
	"context"
	"encoding/json"
	"fmt"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/knotclient"

	"github.com/bluesky-social/jetstream/pkg/models"
)

type Ingester func(ctx context.Context, e *models.Event) error

func (s *Spindle) ingest() Ingester {
	return func(ctx context.Context, e *models.Event) error {
		var err error
		defer func() {
			eventTime := e.TimeUS
			lastTimeUs := eventTime + 1
			if err := s.db.SaveLastTimeUs(lastTimeUs); err != nil {
				err = fmt.Errorf("(deferred) failed to save last time us: %w", err)
			}
		}()

		if e.Kind != models.EventKindCommit {
			return nil
		}

		switch e.Commit.Collection {
		case tangled.SpindleMemberNSID:
			s.ingestMember(ctx, e)
		case tangled.RepoNSID:
			s.ingestRepo(ctx, e)
		}

		return err
	}
}

func (s *Spindle) ingestMember(_ context.Context, e *models.Event) error {
	did := e.Did
	var err error

	l := s.l.With("component", "ingester", "record", tangled.SpindleMemberNSID)

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := e.Commit.Record
		record := tangled.SpindleMember{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "error", err)
			return err
		}

		domain := s.cfg.Server.Hostname
		if s.cfg.Server.Dev {
			domain = s.cfg.Server.ListenAddr
		}
		recordInstance := *record.Instance

		if recordInstance != domain {
			l.Error("domain mismatch", "domain", recordInstance, "expected", domain)
			return fmt.Errorf("domain mismatch: %s != %s", *record.Instance, domain)
		}

		ok, err := s.e.E.Enforce(did, rbacDomain, rbacDomain, "server:invite")
		if err != nil || !ok {
			l.Error("failed to add member", "did", did)
			return fmt.Errorf("failed to enforce permissions: %w", err)
		}

		if err := s.e.AddKnotMember(rbacDomain, record.Subject); err != nil {
			l.Error("failed to add member", "error", err)
			return fmt.Errorf("failed to add member: %w", err)
		}
		l.Info("added member from firehose", "member", record.Subject)

		if err := s.db.AddDid(did); err != nil {
			l.Error("failed to add did", "error", err)
			return fmt.Errorf("failed to add did: %w", err)
		}
		s.jc.AddDid(did)

		return nil

	}
	return nil
}

func (s *Spindle) ingestRepo(_ context.Context, e *models.Event) error {
	var err error

	l := s.l.With("component", "ingester", "record", tangled.RepoNSID)

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := e.Commit.Record
		record := tangled.Repo{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "error", err)
			return err
		}

		domain := s.cfg.Server.Hostname
		if s.cfg.Server.Dev {
			domain = s.cfg.Server.ListenAddr
		}

		// no spindle configured for this repo
		if record.Spindle == nil {
			return nil
		}

		// this repo did not want this spindle
		if *record.Spindle != domain {
			return nil
		}

		// add this repo to the watch list
		if err := s.db.AddRepo(record.Knot, record.Owner, record.Name); err != nil {
			l.Error("failed to add repo", "error", err)
			return fmt.Errorf("failed to add repo: %w", err)
		}

		// add this knot to the event consumer
		src := knotclient.NewEventSource(record.Knot)
		s.ks.AddSource(context.Background(), src)

		return nil

	}
	return nil
}
