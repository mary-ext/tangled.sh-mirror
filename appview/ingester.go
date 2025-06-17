package appview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ipfs/go-cid"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/rbac"
)

type Ingester func(ctx context.Context, e *models.Event) error

func Ingest(d db.DbWrapper, enforcer *rbac.Enforcer) Ingester {
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
		case tangled.RepoArtifactNSID:
			ingestArtifact(&d, e, enforcer)
		case tangled.ActorProfileNSID:
			ingestProfile(&d, e)
		case tangled.SpindleMemberNSID:
			ingestSpindleMember(&d, e, enforcer)
		case tangled.SpindleNSID:
			ingestSpindle(&d, e, true) // TODO: change this to dynamic
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

func ingestArtifact(d *db.DbWrapper, e *models.Event, enforcer *rbac.Enforcer) error {
	did := e.Did
	var err error

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.RepoArtifact{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			log.Printf("invalid record: %s", err)
			return err
		}

		repoAt, err := syntax.ParseATURI(record.Repo)
		if err != nil {
			return err
		}

		repo, err := db.GetRepoByAtUri(d, repoAt.String())
		if err != nil {
			return err
		}

		ok, err := enforcer.E.Enforce(did, repo.Knot, repo.DidSlashRepo(), "repo:push")
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

		err = db.AddArtifact(d, artifact)
	case models.CommitOperationDelete:
		err = db.DeleteArtifact(d, db.FilterEq("did", did), db.FilterEq("rkey", e.Commit.RKey))
	}

	if err != nil {
		return fmt.Errorf("failed to %s artifact record: %w", e.Commit.Operation, err)
	}

	return nil
}

func ingestProfile(d *db.DbWrapper, e *models.Event) error {
	did := e.Did
	var err error

	if e.Commit.RKey != "self" {
		return fmt.Errorf("ingestProfile only ingests `self` record")
	}

	switch e.Commit.Operation {
	case models.CommitOperationCreate, models.CommitOperationUpdate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.ActorProfile{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			log.Printf("invalid record: %s", err)
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

		ddb, ok := d.Execer.(*db.DB)
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
		err = db.DeleteArtifact(d, db.FilterEq("did", did), db.FilterEq("rkey", e.Commit.RKey))
	}

	if err != nil {
		return fmt.Errorf("failed to %s profile record: %w", e.Commit.Operation, err)
	}

	return nil
}

func ingestSpindleMember(_ *db.DbWrapper, e *models.Event, enforcer *rbac.Enforcer) error {
	did := e.Did
	var err error

	switch e.Commit.Operation {
	case models.CommitOperationCreate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.SpindleMember{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			log.Printf("invalid record: %s", err)
			return err
		}

		// only spindle owner can invite to spindles
		ok, err := enforcer.IsSpindleInviteAllowed(did, record.Instance)
		if err != nil || !ok {
			return fmt.Errorf("failed to enforce permissions: %w", err)
		}

		err = enforcer.AddSpindleMember(record.Instance, record.Subject)
		if err != nil {
			return fmt.Errorf("failed to add member: %w", err)
		}
	}

	return nil
}

func ingestSpindle(d *db.DbWrapper, e *models.Event, dev bool) error {
	did := e.Did
	var err error

	switch e.Commit.Operation {
	case models.CommitOperationCreate:
		raw := json.RawMessage(e.Commit.Record)
		record := tangled.Spindle{}
		err = json.Unmarshal(raw, &record)
		if err != nil {
			log.Printf("invalid record: %s", err)
			return err
		}

		// this is a special record whose rkey is the instance of the spindle itself
		instance := e.Commit.RKey

		owner, err := fetchOwner(context.TODO(), instance, dev)
		if err != nil {
			log.Printf("failed to verify owner of %s: %s", instance, err)
			return err
		}

		// verify that the spindle owner points back to this did
		if owner != did {
			log.Printf("incorrect owner for domain: %s, %s != %s", instance, owner, did)
			return err
		}

		// mark this spindle as registered
		ddb, ok := d.Execer.(*db.DB)
		if !ok {
			return fmt.Errorf("failed to index profile record, invalid db cast")
		}

		_, err = db.VerifySpindle(
			ddb,
			db.FilterEq("owner", did),
			db.FilterEq("instance", instance),
		)

		return err
	}

	return nil
}

func fetchOwner(ctx context.Context, domain string, dev bool) (string, error) {
	scheme := "https"
	if dev {
		scheme = "http"
	}

	url := fmt.Sprintf("%s://%s/owner", scheme, domain)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil || resp.StatusCode != 200 {
		return "", errors.New("failed to fetch /owner")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024)) // read atmost 1kb of data
	if err != nil {
		return "", fmt.Errorf("failed to read /owner response: %w", err)
	}

	did := strings.TrimSpace(string(body))
	if did == "" {
		return "", errors.New("empty DID in /owner response")
	}

	return did, nil
}
