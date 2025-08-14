package appview

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ipfs/go-cid"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/serververify"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/rbac"
)

type Ingester struct {
	Db         db.DbWrapper
	Enforcer   *rbac.Enforcer
	IdResolver *idresolver.Resolver
	Config     *config.Config
	Logger     *slog.Logger
}

type processFunc func(ctx context.Context, e *models.Event) error

func (i *Ingester) Ingest() processFunc {
	return func(ctx context.Context, e *models.Event) error {
		var err error
		defer func() {
			eventTime := e.TimeUS
			lastTimeUs := eventTime + 1
			if err := i.Db.SaveLastTimeUs(lastTimeUs); err != nil {
				err = fmt.Errorf("(deferred) failed to save last time us: %w", err)
			}
		}()

		l := i.Logger.With("kind", e.Kind)
		switch e.Kind {
		case models.EventKindAccount:
			if !e.Account.Active && *e.Account.Status == "deactivated" {
				err = i.IdResolver.InvalidateIdent(ctx, e.Account.Did)
			}
		case models.EventKindIdentity:
			err = i.IdResolver.InvalidateIdent(ctx, e.Identity.Did)
		case models.EventKindCommit:
			switch e.Commit.Collection {
			case tangled.GraphFollowNSID:
				err = i.ingestFollow(e)
			case tangled.FeedStarNSID:
				err = i.ingestStar(e)
			case tangled.PublicKeyNSID:
				err = i.ingestPublicKey(e)
			case tangled.RepoArtifactNSID:
				err = i.ingestArtifact(e)
			case tangled.ActorProfileNSID:
				err = i.ingestProfile(e)
			case tangled.SpindleMemberNSID:
				err = i.ingestSpindleMember(e)
			case tangled.SpindleNSID:
				err = i.ingestSpindle(e)
			case tangled.StringNSID:
				err = i.ingestString(e)
			}
			l = i.Logger.With("nsid", e.Commit.Collection)
		}

		if err != nil {
			l.Debug("error ingesting record", "err", err)
		}

		return nil
	}
}

func (i *Ingester) ingestStar(e *models.Event) error {
	var err error
	did := e.Did

	l := i.Logger.With("handler", "ingestStar")
	l = l.With("nsid", e.Commit.Collection)

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		var subjectUri syntax.ATURI

		raw := json.RawMessage(e.Commit.Record)
		record := tangled.FeedStar{}
		err := json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		subjectUri, err = syntax.ParseATURI(record.Subject)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}
		err = db.AddStar(i.Db, &db.Star{
			StarredByDid: did,
			RepoAt:       subjectUri,
			Rkey:         e.Commit.RKey,
		})
	case models.CommitOperationDelete:
		err = db.DeleteStarByRkey(i.Db, did, e.Commit.RKey)
	}

	if err != nil {
		return fmt.Errorf("failed to %s star record: %w", e.Commit.Operation, err)
	}

	return nil
}

func (i *Ingester) ingestFollow(e *models.Event) error {
	var err error
	did := e.Did

	l := i.Logger.With("handler", "ingestFollow")
	l = l.With("nsid", e.Commit.Collection)

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.GraphFollow{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		err = db.AddFollow(i.Db, &db.Follow{
			UserDid:    did,
			SubjectDid: record.Subject,
			Rkey:       e.Commit.RKey,
		})
	case models.CommitOperationDelete:
		err = db.DeleteFollowByRkey(i.Db, did, e.Commit.RKey)
	}

	if err != nil {
		return fmt.Errorf("failed to %s follow record: %w", e.Commit.Operation, err)
	}

	return nil
}

func (i *Ingester) ingestPublicKey(e *models.Event) error {
	did := e.Did
	var err error

	l := i.Logger.With("handler", "ingestPublicKey")
	l = l.With("nsid", e.Commit.Collection)

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		l.Debug("processing add of pubkey")
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.PublicKey{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		name := record.Name
		key := record.Key
		err = db.AddPublicKey(i.Db, did, name, key, e.Commit.RKey)
	case models.CommitOperationDelete:
		l.Debug("processing delete of pubkey")
		err = db.DeletePublicKeyByRkey(i.Db, did, e.Commit.RKey)
	}

	if err != nil {
		return fmt.Errorf("failed to %s pubkey record: %w", e.Commit.Operation, err)
	}

	return nil
}

func (i *Ingester) ingestArtifact(e *models.Event) error {
	did := e.Did
	var err error

	l := i.Logger.With("handler", "ingestArtifact")
	l = l.With("nsid", e.Commit.Collection)

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.RepoArtifact{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		repoAt, err := syntax.ParseATURI(record.Repo)
		if err != nil {
			return err
		}

		repo, err := db.GetRepoByAtUri(i.Db, repoAt.String())
		if err != nil {
			return err
		}

		ok, err := i.Enforcer.E.Enforce(did, repo.Knot, repo.DidSlashRepo(), "repo:push")
		if err != nil || !ok {
			return err
		}

		createdAt, err := time.Parse(time.RFC3339, record.CreatedAt)
		if err != nil {
			createdAt = time.Now()
		}

		artifact := db.Artifact{
			Did:       did,
			Rkey:      e.Commit.RKey,
			RepoAt:    repoAt,
			Tag:       plumbing.Hash(record.Tag),
			CreatedAt: createdAt,
			BlobCid:   cid.Cid(record.Artifact.Ref),
			Name:      record.Name,
			Size:      uint64(record.Artifact.Size),
			MimeType:  record.Artifact.MimeType,
		}

		err = db.AddArtifact(i.Db, artifact)
	case models.CommitOperationDelete:
		err = db.DeleteArtifact(i.Db, db.FilterEq("did", did), db.FilterEq("rkey", e.Commit.RKey))
	}

	if err != nil {
		return fmt.Errorf("failed to %s artifact record: %w", e.Commit.Operation, err)
	}

	return nil
}

func (i *Ingester) ingestProfile(e *models.Event) error {
	did := e.Did
	var err error

	l := i.Logger.With("handler", "ingestProfile")
	l = l.With("nsid", e.Commit.Collection)

	if e.Commit.RKey != "self" {
		return fmt.Errorf("ingestProfile only ingests `self` record")
	}

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.ActorProfile{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		description := ""
		if record.Description != nil {
			description = *record.Description
		}

		includeBluesky := record.Bluesky

		location := ""
		if record.Location != nil {
			location = *record.Location
		}

		var links [5]string
		for i, l := range record.Links {
			if i < 5 {
				links[i] = l
			}
		}

		var stats [2]db.VanityStat
		for i, s := range record.Stats {
			if i < 2 {
				stats[i].Kind = db.VanityStatKind(s)
			}
		}

		var pinned [6]syntax.ATURI
		for i, r := range record.PinnedRepositories {
			if i < 6 {
				pinned[i] = syntax.ATURI(r)
			}
		}

		profile := db.Profile{
			Did:            did,
			Description:    description,
			IncludeBluesky: includeBluesky,
			Location:       location,
			Links:          links,
			Stats:          stats,
			PinnedRepos:    pinned,
		}

		ddb, ok := i.Db.Execer.(*db.DB)
		if !ok {
			return fmt.Errorf("failed to index profile record, invalid db cast")
		}

		tx, err := ddb.Begin()
		if err != nil {
			return fmt.Errorf("failed to start transaction")
		}

		err = db.ValidateProfile(tx, &profile)
		if err != nil {
			return fmt.Errorf("invalid profile record")
		}

		err = db.UpsertProfile(tx, &profile)
	case models.CommitOperationDelete:
		err = db.DeleteArtifact(i.Db, db.FilterEq("did", did), db.FilterEq("rkey", e.Commit.RKey))
	}

	if err != nil {
		return fmt.Errorf("failed to %s profile record: %w", e.Commit.Operation, err)
	}

	return nil
}

func (i *Ingester) ingestSpindleMember(e *models.Event) error {
	did := e.Did
	var err error

	l := i.Logger.With("handler", "ingestSpindleMember")
	l = l.With("nsid", e.Commit.Collection)

	switch e.Commit.Operation {
	case models.CommitOperationCreate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.SpindleMember{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		// only spindle owner can invite to spindles
		ok, err := i.Enforcer.IsSpindleInviteAllowed(did, record.Instance)
		if err != nil || !ok {
			return fmt.Errorf("failed to enforce permissions: %w", err)
		}

		memberId, err := i.IdResolver.ResolveIdent(context.Background(), record.Subject)
		if err != nil {
			return err
		}

		if memberId.Handle.IsInvalidHandle() {
			return err
		}

		ddb, ok := i.Db.Execer.(*db.DB)
		if !ok {
			return fmt.Errorf("failed to index profile record, invalid db cast")
		}

		err = db.AddSpindleMember(ddb, db.SpindleMember{
			Did:      syntax.DID(did),
			Rkey:     e.Commit.RKey,
			Instance: record.Instance,
			Subject:  memberId.DID,
		})
		if !ok {
			return fmt.Errorf("failed to add to db: %w", err)
		}

		err = i.Enforcer.AddSpindleMember(record.Instance, memberId.DID.String())
		if err != nil {
			return fmt.Errorf("failed to update ACLs: %w", err)
		}

		l.Info("added spindle member")
	case models.CommitOperationDelete:
		rkey := e.Commit.RKey

		ddb, ok := i.Db.Execer.(*db.DB)
		if !ok {
			return fmt.Errorf("failed to index profile record, invalid db cast")
		}

		// get record from db first
		members, err := db.GetSpindleMembers(
			ddb,
			db.FilterEq("did", did),
			db.FilterEq("rkey", rkey),
		)
		if err != nil || len(members) != 1 {
			return fmt.Errorf("failed to get member: %w, len(members) = %d", err, len(members))
		}
		member := members[0]

		tx, err := ddb.Begin()
		if err != nil {
			return fmt.Errorf("failed to start txn: %w", err)
		}

		// remove record by rkey && update enforcer
		if err = db.RemoveSpindleMember(
			tx,
			db.FilterEq("did", did),
			db.FilterEq("rkey", rkey),
		); err != nil {
			return fmt.Errorf("failed to remove from db: %w", err)
		}

		// update enforcer
		err = i.Enforcer.RemoveSpindleMember(member.Instance, member.Subject.String())
		if err != nil {
			return fmt.Errorf("failed to update ACLs: %w", err)
		}

		if err = tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit txn: %w", err)
		}

		if err = i.Enforcer.E.SavePolicy(); err != nil {
			return fmt.Errorf("failed to save ACLs: %w", err)
		}

		l.Info("removed spindle member")
	}

	return nil
}

func (i *Ingester) ingestSpindle(e *models.Event) error {
	did := e.Did
	var err error

	l := i.Logger.With("handler", "ingestSpindle")
	l = l.With("nsid", e.Commit.Collection)

	switch e.Commit.Operation {
	case models.CommitOperationCreate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.Spindle{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		instance := e.Commit.RKey

		ddb, ok := i.Db.Execer.(*db.DB)
		if !ok {
			return fmt.Errorf("failed to index profile record, invalid db cast")
		}

		err := db.AddSpindle(ddb, db.Spindle{
			Owner:    syntax.DID(did),
			Instance: instance,
		})
		if err != nil {
			l.Error("failed to add spindle to db", "err", err, "instance", instance)
			return err
		}

		err = serververify.RunVerification(context.Background(), instance, did, i.Config.Core.Dev)
		if err != nil {
			l.Error("failed to add spindle to db", "err", err, "instance", instance)
			return err
		}

		_, err = serververify.MarkSpindleVerified(ddb, i.Enforcer, instance, did)
		if err != nil {
			return fmt.Errorf("failed to mark verified: %w", err)
		}

		return nil

	case models.CommitOperationDelete:
		instance := e.Commit.RKey

		ddb, ok := i.Db.Execer.(*db.DB)
		if !ok {
			return fmt.Errorf("failed to index profile record, invalid db cast")
		}

		// get record from db first
		spindles, err := db.GetSpindles(
			ddb,
			db.FilterEq("owner", did),
			db.FilterEq("instance", instance),
		)
		if err != nil || len(spindles) != 1 {
			return fmt.Errorf("failed to get spindles: %w, len(spindles) = %d", err, len(spindles))
		}
		spindle := spindles[0]

		tx, err := ddb.Begin()
		if err != nil {
			return err
		}
		defer func() {
			tx.Rollback()
			i.Enforcer.E.LoadPolicy()
		}()

		// remove spindle members first
		err = db.RemoveSpindleMember(
			tx,
			db.FilterEq("owner", did),
			db.FilterEq("instance", instance),
		)
		if err != nil {
			return err
		}

		err = db.DeleteSpindle(
			tx,
			db.FilterEq("owner", did),
			db.FilterEq("instance", instance),
		)
		if err != nil {
			return err
		}

		if spindle.Verified != nil {
			err = i.Enforcer.RemoveSpindle(instance)
			if err != nil {
				return err
			}
		}

		err = tx.Commit()
		if err != nil {
			return err
		}

		err = i.Enforcer.E.SavePolicy()
		if err != nil {
			return err
		}
	}

	return nil
}

func (i *Ingester) ingestString(e *models.Event) error {
	did := e.Did
	rkey := e.Commit.RKey

	var err error

	l := i.Logger.With("handler", "ingestString", "nsid", e.Commit.Collection, "did", did, "rkey", rkey)
	l.Info("ingesting record")

	ddb, ok := i.Db.Execer.(*db.DB)
	if !ok {
		return fmt.Errorf("failed to index string record, invalid db cast")
	}

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.String{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		string := db.StringFromRecord(did, rkey, record)

		if err = string.Validate(); err != nil {
			l.Error("invalid record", "err", err)
			return err
		}

		if err = db.AddString(ddb, string); err != nil {
			l.Error("failed to add string", "err", err)
			return err
		}

		return nil

	case models.CommitOperationDelete:
		if err := db.DeleteString(
			ddb,
			db.FilterEq("did", did),
			db.FilterEq("rkey", rkey),
		); err != nil {
			l.Error("failed to delete", "err", err)
			return fmt.Errorf("failed to delete string record: %w", err)
		}

		return nil
	}

	return nil
}
