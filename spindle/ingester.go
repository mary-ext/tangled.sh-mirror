package spindle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/db"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/bluesky-social/jetstream/pkg/models"
	securejoin "github.com/cyphar/filepath-securejoin"
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
			err = s.ingestMember(ctx, e)
		case tangled.RepoNSID:
			err = s.ingestRepo(ctx, e)
		case tangled.RepoCollaboratorNSID:
			err = s.ingestCollaborator(ctx, e)
		}

		if err != nil {
			s.l.Debug("failed to process message", "nsid", e.Commit.Collection, "err", err)
		}

		return nil
	}
}

func (s *Spindle) ingestMember(_ context.Context, e *models.Event) error {
	var err error
	did := e.Did
	rkey := e.Commit.RKey

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
		recordInstance := record.Instance

		if recordInstance != domain {
			l.Error("domain mismatch", "domain", recordInstance, "expected", domain)
			return fmt.Errorf("domain mismatch: %s != %s", record.Instance, domain)
		}

		ok, err := s.e.IsSpindleInviteAllowed(did, rbacDomain)
		if err != nil || !ok {
			l.Error("failed to add member", "did", did, "error", err)
			return fmt.Errorf("failed to enforce permissions: %w", err)
		}

		if err := db.AddSpindleMember(s.db, db.SpindleMember{
			Did:      syntax.DID(did),
			Rkey:     rkey,
			Instance: recordInstance,
			Subject:  syntax.DID(record.Subject),
			Created:  time.Now(),
		}); err != nil {
			l.Error("failed to add member", "error", err)
			return fmt.Errorf("failed to add member: %w", err)
		}

		if err := s.e.AddSpindleMember(rbacDomain, record.Subject); err != nil {
			l.Error("failed to add member", "error", err)
			return fmt.Errorf("failed to add member: %w", err)
		}
		l.Info("added member from firehose", "member", record.Subject)

		if err := s.db.AddDid(record.Subject); err != nil {
			l.Error("failed to add did", "error", err)
			return fmt.Errorf("failed to add did: %w", err)
		}
		s.jc.AddDid(record.Subject)

		return nil

	case models.CommitOperationDelete:
		record, err := db.GetSpindleMember(s.db, did, rkey)
		if err != nil {
			l.Error("failed to find member", "error", err)
			return fmt.Errorf("failed to find member: %w", err)
		}

		if err := db.RemoveSpindleMember(s.db, did, rkey); err != nil {
			l.Error("failed to remove member", "error", err)
			return fmt.Errorf("failed to remove member: %w", err)
		}

		if err := s.e.RemoveSpindleMember(rbacDomain, record.Subject.String()); err != nil {
			l.Error("failed to add member", "error", err)
			return fmt.Errorf("failed to add member: %w", err)
		}
		l.Info("added member from firehose", "member", record.Subject)

		if err := s.db.RemoveDid(record.Subject.String()); err != nil {
			l.Error("failed to add did", "error", err)
			return fmt.Errorf("failed to add did: %w", err)
		}
		s.jc.RemoveDid(record.Subject.String())

	}
	return nil
}

func (s *Spindle) ingestRepo(ctx context.Context, e *models.Event) error {
	var err error
	did := e.Did
	resolver := idresolver.DefaultResolver()

	l := s.l.With("component", "ingester", "record", tangled.RepoNSID)

	l.Info("ingesting repo record")

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

		// no spindle configured for this repo
		if record.Spindle == nil {
			l.Info("no spindle configured", "did", record.Owner, "name", record.Name)
			return nil
		}

		// this repo did not want this spindle
		if *record.Spindle != domain {
			l.Info("different spindle configured", "did", record.Owner, "name", record.Name, "spindle", *record.Spindle, "domain", domain)
			return nil
		}

		// add this repo to the watch list
		if err := s.db.AddRepo(record.Knot, record.Owner, record.Name); err != nil {
			l.Error("failed to add repo", "error", err)
			return fmt.Errorf("failed to add repo: %w", err)
		}

		didSlashRepo, err := securejoin.SecureJoin(record.Owner, record.Name)
		if err != nil {
			return err
		}

		// add repo to rbac
		if err := s.e.AddRepo(record.Owner, rbac.ThisServer, didSlashRepo); err != nil {
			l.Error("failed to add repo to enforcer", "error", err)
			return fmt.Errorf("failed to add repo: %w", err)
		}

		// add collaborators to rbac
		owner, err := resolver.ResolveIdent(ctx, did)
		if err != nil || owner.Handle.IsInvalidHandle() {
			return err
		}
		if err := s.fetchAndAddCollaborators(ctx, owner, didSlashRepo); err != nil {
			return err
		}

		// add this knot to the event consumer
		src := eventconsumer.NewKnotSource(record.Knot)
		s.ks.AddSource(context.Background(), src)

		return nil

	}
	return nil
}

func (s *Spindle) ingestCollaborator(ctx context.Context, e *models.Event) error {
	var err error

	l := s.l.With("component", "ingester", "record", tangled.RepoCollaboratorNSID, "did", e.Did)

	l.Info("ingesting collaborator record")

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := e.Commit.Record
		record := tangled.RepoCollaborator{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "error", err)
			return err
		}

		resolver := idresolver.DefaultResolver()

		subjectId, err := resolver.ResolveIdent(ctx, record.Subject)
		if err != nil || subjectId.Handle.IsInvalidHandle() {
			return err
		}

		repoAt, err := syntax.ParseATURI(record.Repo)
		if err != nil {
			l.Info("rejecting record, invalid repoAt", "repoAt", record.Repo)
			return nil
		}

		// TODO: get rid of this entirely
		// resolve this aturi to extract the repo record
		owner, err := resolver.ResolveIdent(ctx, repoAt.Authority().String())
		if err != nil || owner.Handle.IsInvalidHandle() {
			return fmt.Errorf("failed to resolve handle: %w", err)
		}

		xrpcc := xrpc.Client{
			Host: owner.PDSEndpoint(),
		}

		resp, err := comatproto.RepoGetRecord(ctx, &xrpcc, "", tangled.RepoNSID, repoAt.Authority().String(), repoAt.RecordKey().String())
		if err != nil {
			return err
		}

		repo := resp.Value.Val.(*tangled.Repo)
		didSlashRepo, _ := securejoin.SecureJoin(owner.DID.String(), repo.Name)

		// check perms for this user
		if ok, err := s.e.IsCollaboratorInviteAllowed(owner.DID.String(), rbac.ThisServer, didSlashRepo); !ok || err != nil {
			return fmt.Errorf("insufficient permissions: %w", err)
		}

		// add collaborator to rbac
		if err := s.e.AddCollaborator(record.Subject, rbac.ThisServer, didSlashRepo); err != nil {
			l.Error("failed to add repo to enforcer", "error", err)
			return fmt.Errorf("failed to add repo: %w", err)
		}

		return nil
	}
	return nil
}

func (s *Spindle) fetchAndAddCollaborators(ctx context.Context, owner *identity.Identity, didSlashRepo string) error {
	l := s.l.With("component", "ingester", "handler", "fetchAndAddCollaborators")

	l.Info("fetching and adding existing collaborators")

	xrpcc := xrpc.Client{
		Host: owner.PDSEndpoint(),
	}

	resp, err := comatproto.RepoListRecords(ctx, &xrpcc, tangled.RepoCollaboratorNSID, "", 50, owner.DID.String(), false)
	if err != nil {
		return err
	}

	var errs error
	for _, r := range resp.Records {
		if r == nil {
			continue
		}
		record := r.Value.Val.(*tangled.RepoCollaborator)

		if err := s.e.AddCollaborator(record.Subject, rbac.ThisServer, didSlashRepo); err != nil {
			l.Error("failed to add repo to enforcer", "error", err)
			errors.Join(errs, fmt.Errorf("failed to add repo: %w", err))
		}
	}

	return errs
}
