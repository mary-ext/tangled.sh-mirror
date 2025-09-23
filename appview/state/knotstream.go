package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/cache"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	ec "tangled.org/core/eventconsumer"
	"tangled.org/core/eventconsumer/cursor"
	"tangled.org/core/log"
	"tangled.org/core/rbac"
	"tangled.org/core/workflow"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/posthog/posthog-go"
)

func Knotstream(ctx context.Context, c *config.Config, d *db.DB, enforcer *rbac.Enforcer, posthog posthog.Client) (*ec.Consumer, error) {
	knots, err := db.GetRegistrations(
		d,
		db.FilterIsNot("registered", "null"),
	)
	if err != nil {
		return nil, err
	}

	srcs := make(map[ec.Source]struct{})
	for _, k := range knots {
		s := ec.NewKnotSource(k.Domain)
		srcs[s] = struct{}{}
	}

	logger := log.New("knotstream")
	cache := cache.New(c.Redis.Addr)
	cursorStore := cursor.NewRedisCursorStore(cache)

	cfg := ec.ConsumerConfig{
		Sources:           srcs,
		ProcessFunc:       knotIngester(d, enforcer, posthog, c.Core.Dev),
		RetryInterval:     c.Knotstream.RetryInterval,
		MaxRetryInterval:  c.Knotstream.MaxRetryInterval,
		ConnectionTimeout: c.Knotstream.ConnectionTimeout,
		WorkerCount:       c.Knotstream.WorkerCount,
		QueueSize:         c.Knotstream.QueueSize,
		Logger:            logger,
		Dev:               c.Core.Dev,
		CursorStore:       &cursorStore,
	}

	return ec.NewConsumer(cfg), nil
}

func knotIngester(d *db.DB, enforcer *rbac.Enforcer, posthog posthog.Client, dev bool) ec.ProcessFunc {
	return func(ctx context.Context, source ec.Source, msg ec.Message) error {
		switch msg.Nsid {
		case tangled.GitRefUpdateNSID:
			return ingestRefUpdate(d, enforcer, posthog, dev, source, msg)
		case tangled.PipelineNSID:
			return ingestPipeline(d, source, msg)
		}

		return nil
	}
}

func ingestRefUpdate(d *db.DB, enforcer *rbac.Enforcer, pc posthog.Client, dev bool, source ec.Source, msg ec.Message) error {
	var record tangled.GitRefUpdate
	err := json.Unmarshal(msg.EventJson, &record)
	if err != nil {
		return err
	}

	knownKnots, err := enforcer.GetKnotsForUser(record.CommitterDid)
	if err != nil {
		return err
	}
	if !slices.Contains(knownKnots, source.Key()) {
		return fmt.Errorf("%s does not belong to %s, something is fishy", record.CommitterDid, source.Key())
	}

	err1 := populatePunchcard(d, record)
	err2 := updateRepoLanguages(d, record)

	var err3 error
	if !dev {
		err3 = pc.Enqueue(posthog.Capture{
			DistinctId: record.CommitterDid,
			Event:      "git_ref_update",
		})
	}

	return errors.Join(err1, err2, err3)
}

func populatePunchcard(d *db.DB, record tangled.GitRefUpdate) error {
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

	punch := models.Punch{
		Did:   record.CommitterDid,
		Date:  time.Now(),
		Count: count,
	}
	return db.AddPunch(d, punch)
}

func updateRepoLanguages(d *db.DB, record tangled.GitRefUpdate) error {
	if record.Meta == nil || record.Meta.LangBreakdown == nil || record.Meta.LangBreakdown.Inputs == nil {
		return fmt.Errorf("empty language data for repo: %s/%s", record.RepoDid, record.RepoName)
	}

	repos, err := db.GetRepos(
		d,
		0,
		db.FilterEq("did", record.RepoDid),
		db.FilterEq("name", record.RepoName),
	)
	if err != nil {
		return fmt.Errorf("failed to look for repo in DB (%s/%s): %w", record.RepoDid, record.RepoName, err)
	}
	if len(repos) != 1 {
		return fmt.Errorf("incorrect number of repos returned: %d (expected 1)", len(repos))
	}
	repo := repos[0]

	ref := plumbing.ReferenceName(record.Ref)
	if !ref.IsBranch() {
		return fmt.Errorf("%s is not a valid reference name", ref)
	}

	var langs []models.RepoLanguage
	for _, l := range record.Meta.LangBreakdown.Inputs {
		if l == nil {
			continue
		}

		langs = append(langs, models.RepoLanguage{
			RepoAt:       repo.RepoAt(),
			Ref:          ref.Short(),
			IsDefaultRef: record.Meta.IsDefaultRef,
			Language:     l.Lang,
			Bytes:        l.Size,
		})
	}

	return db.InsertRepoLanguages(d, langs)
}

func ingestPipeline(d *db.DB, source ec.Source, msg ec.Message) error {
	var record tangled.Pipeline
	err := json.Unmarshal(msg.EventJson, &record)
	if err != nil {
		return err
	}

	if record.TriggerMetadata == nil {
		return fmt.Errorf("empty trigger metadata: nsid %s, rkey %s", msg.Nsid, msg.Rkey)
	}

	if record.TriggerMetadata.Repo == nil {
		return fmt.Errorf("empty repo: nsid %s, rkey %s", msg.Nsid, msg.Rkey)
	}

	// does this repo have a spindle configured?
	repos, err := db.GetRepos(
		d,
		0,
		db.FilterEq("did", record.TriggerMetadata.Repo.Did),
		db.FilterEq("name", record.TriggerMetadata.Repo.Repo),
	)
	if err != nil {
		return fmt.Errorf("failed to look for repo in DB: nsid %s, rkey %s, %w", msg.Nsid, msg.Rkey, err)
	}
	if len(repos) != 1 {
		return fmt.Errorf("incorrect number of repos returned: %d (expected 1)", len(repos))
	}
	if repos[0].Spindle == "" {
		return fmt.Errorf("repo does not have a spindle configured yet: nsid %s, rkey %s", msg.Nsid, msg.Rkey)
	}

	// trigger info
	var trigger models.Trigger
	var sha string
	trigger.Kind = workflow.TriggerKind(record.TriggerMetadata.Kind)
	switch trigger.Kind {
	case workflow.TriggerKindPush:
		trigger.PushRef = &record.TriggerMetadata.Push.Ref
		trigger.PushNewSha = &record.TriggerMetadata.Push.NewSha
		trigger.PushOldSha = &record.TriggerMetadata.Push.OldSha
		sha = *trigger.PushNewSha
	case workflow.TriggerKindPullRequest:
		trigger.PRSourceBranch = &record.TriggerMetadata.PullRequest.SourceBranch
		trigger.PRTargetBranch = &record.TriggerMetadata.PullRequest.TargetBranch
		trigger.PRSourceSha = &record.TriggerMetadata.PullRequest.SourceSha
		trigger.PRAction = &record.TriggerMetadata.PullRequest.Action
		sha = *trigger.PRSourceSha
	}

	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("failed to start txn: %w", err)
	}

	triggerId, err := db.AddTrigger(tx, trigger)
	if err != nil {
		return fmt.Errorf("failed to add trigger entry: %w", err)
	}

	pipeline := models.Pipeline{
		Rkey:      msg.Rkey,
		Knot:      source.Key(),
		RepoOwner: syntax.DID(record.TriggerMetadata.Repo.Did),
		RepoName:  record.TriggerMetadata.Repo.Repo,
		TriggerId: int(triggerId),
		Sha:       sha,
	}

	err = db.AddPipeline(tx, pipeline)
	if err != nil {
		return fmt.Errorf("failed to add pipeline: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit txn: %w", err)
	}

	return nil
}
