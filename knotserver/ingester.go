package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/bluesky-social/jetstream/pkg/models"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.org/core/api/tangled"
	"tangled.org/core/idresolver"
	"tangled.org/core/knotserver/db"
	"tangled.org/core/knotserver/git"
	"tangled.org/core/log"
	"tangled.org/core/rbac"
	"tangled.org/core/workflow"
)

func (h *Knot) processPublicKey(ctx context.Context, event *models.Event) error {
	l := log.FromContext(ctx)
	raw := json.RawMessage(event.Commit.Record)
	did := event.Did

	var record tangled.PublicKey
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("failed to unmarshal record: %w", err)
	}

	pk := db.PublicKey{
		Did:       did,
		PublicKey: record,
	}
	if err := h.db.AddPublicKey(pk); err != nil {
		l.Error("failed to add public key", "error", err)
		return fmt.Errorf("failed to add public key: %w", err)
	}
	l.Info("added public key from firehose", "did", did)
	return nil
}

func (h *Knot) processKnotMember(ctx context.Context, event *models.Event) error {
	l := log.FromContext(ctx)
	raw := json.RawMessage(event.Commit.Record)
	did := event.Did

	var record tangled.KnotMember
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("failed to unmarshal record: %w", err)
	}

	if record.Domain != h.c.Server.Hostname {
		l.Error("domain mismatch", "domain", record.Domain, "expected", h.c.Server.Hostname)
		return fmt.Errorf("domain mismatch: %s != %s", record.Domain, h.c.Server.Hostname)
	}

	ok, err := h.e.E.Enforce(did, rbac.ThisServer, rbac.ThisServer, "server:invite")
	if err != nil || !ok {
		l.Error("failed to add member", "did", did)
		return fmt.Errorf("failed to enforce permissions: %w", err)
	}

	if err := h.e.AddKnotMember(rbac.ThisServer, record.Subject); err != nil {
		l.Error("failed to add member", "error", err)
		return fmt.Errorf("failed to add member: %w", err)
	}
	l.Info("added member from firehose", "member", record.Subject)

	if err := h.db.AddDid(record.Subject); err != nil {
		l.Error("failed to add did", "error", err)
		return fmt.Errorf("failed to add did: %w", err)
	}
	h.jc.AddDid(record.Subject)

	if err := h.fetchAndAddKeys(ctx, record.Subject); err != nil {
		return fmt.Errorf("failed to fetch and add keys: %w", err)
	}

	return nil
}

func (h *Knot) processPull(ctx context.Context, event *models.Event) error {
	raw := json.RawMessage(event.Commit.Record)
	did := event.Did

	var record tangled.RepoPull
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("failed to unmarshal record: %w", err)
	}

	l := log.FromContext(ctx)
	l = l.With("handler", "processPull")
	l = l.With("did", did)

	if record.Target == nil {
		return fmt.Errorf("ignoring pull record: target repo is nil")
	}

	l = l.With("target_repo", record.Target.Repo)
	l = l.With("target_branch", record.Target.Branch)

	if record.Source == nil {
		return fmt.Errorf("ignoring pull record: not a branch-based pull request")
	}

	if record.Source.Repo != nil {
		return fmt.Errorf("ignoring pull record: fork based pull")
	}

	repoAt, err := syntax.ParseATURI(record.Target.Repo)
	if err != nil {
		return fmt.Errorf("failed to parse ATURI: %w", err)
	}

	// resolve this aturi to extract the repo record
	resolver := idresolver.DefaultResolver()
	ident, err := resolver.ResolveIdent(ctx, repoAt.Authority().String())
	if err != nil || ident.Handle.IsInvalidHandle() {
		return fmt.Errorf("failed to resolve handle: %w", err)
	}

	xrpcc := xrpc.Client{
		Host: ident.PDSEndpoint(),
	}

	resp, err := comatproto.RepoGetRecord(ctx, &xrpcc, "", tangled.RepoNSID, repoAt.Authority().String(), repoAt.RecordKey().String())
	if err != nil {
		return fmt.Errorf("failed to resolver repo: %w", err)
	}

	repo := resp.Value.Val.(*tangled.Repo)

	if repo.Knot != h.c.Server.Hostname {
		return fmt.Errorf("rejected pull record: not this knot, %s != %s", repo.Knot, h.c.Server.Hostname)
	}

	didSlashRepo, err := securejoin.SecureJoin(ident.DID.String(), repo.Name)
	if err != nil {
		return fmt.Errorf("failed to construct relative repo path: %w", err)
	}

	repoPath, err := securejoin.SecureJoin(h.c.Repo.ScanPath, didSlashRepo)
	if err != nil {
		return fmt.Errorf("failed to construct absolute repo path: %w", err)
	}

	gr, err := git.Open(repoPath, record.Source.Sha)
	if err != nil {
		return fmt.Errorf("failed to open git repository: %w", err)
	}

	workflowDir, err := gr.FileTree(ctx, workflow.WorkflowDir)
	if err != nil {
		return fmt.Errorf("failed to open workflow directory: %w", err)
	}

	var pipeline workflow.RawPipeline
	for _, e := range workflowDir {
		if !e.IsFile {
			continue
		}

		fpath := filepath.Join(workflow.WorkflowDir, e.Name)
		contents, err := gr.RawContent(fpath)
		if err != nil {
			continue
		}

		pipeline = append(pipeline, workflow.RawWorkflow{
			Name:     e.Name,
			Contents: contents,
		})
	}

	trigger := tangled.Pipeline_PullRequestTriggerData{
		Action:       "create",
		SourceBranch: record.Source.Branch,
		SourceSha:    record.Source.Sha,
		TargetBranch: record.Target.Branch,
	}

	compiler := workflow.Compiler{
		Trigger: tangled.Pipeline_TriggerMetadata{
			Kind:        string(workflow.TriggerKindPullRequest),
			PullRequest: &trigger,
			Repo: &tangled.Pipeline_TriggerRepo{
				Did:  ident.DID.String(),
				Knot: repo.Knot,
				Repo: repo.Name,
			},
		},
	}

	cp := compiler.Compile(compiler.Parse(pipeline))
	eventJson, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("failed to marshal pipeline event: %w", err)
	}

	// do not run empty pipelines
	if cp.Workflows == nil {
		return nil
	}

	ev := db.Event{
		Rkey:      TID(),
		Nsid:      tangled.PipelineNSID,
		EventJson: string(eventJson),
	}

	return h.db.InsertEvent(ev, h.n)
}

// duplicated from add collaborator
func (h *Knot) processCollaborator(ctx context.Context, event *models.Event) error {
	raw := json.RawMessage(event.Commit.Record)
	did := event.Did

	var record tangled.RepoCollaborator
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("failed to unmarshal record: %w", err)
	}

	repoAt, err := syntax.ParseATURI(record.Repo)
	if err != nil {
		return err
	}

	resolver := idresolver.DefaultResolver()

	subjectId, err := resolver.ResolveIdent(ctx, record.Subject)
	if err != nil || subjectId.Handle.IsInvalidHandle() {
		return err
	}

	// TODO: fix this for good, we need to fetch the record here unfortunately
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
	ok, err := h.e.IsCollaboratorInviteAllowed(did, rbac.ThisServer, didSlashRepo)
	if err != nil {
		return fmt.Errorf("failed to check permissions: %w", err)
	}
	if !ok {
		return fmt.Errorf("insufficient permissions: %s, %s, %s", did, "IsCollaboratorInviteAllowed", didSlashRepo)
	}

	if err := h.db.AddDid(subjectId.DID.String()); err != nil {
		return err
	}
	h.jc.AddDid(subjectId.DID.String())

	if err := h.e.AddCollaborator(subjectId.DID.String(), rbac.ThisServer, didSlashRepo); err != nil {
		return err
	}

	return h.fetchAndAddKeys(ctx, subjectId.DID.String())
}

func (h *Knot) fetchAndAddKeys(ctx context.Context, did string) error {
	l := log.FromContext(ctx)

	keysEndpoint, err := url.JoinPath(h.c.AppViewEndpoint, "keys", did)
	if err != nil {
		l.Error("error building endpoint url", "did", did, "error", err.Error())
		return fmt.Errorf("error building endpoint url: %w", err)
	}

	resp, err := http.Get(keysEndpoint)
	if err != nil {
		l.Error("error getting keys", "did", did, "error", err)
		return fmt.Errorf("error getting keys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		l.Info("no keys found for did", "did", did)
		return nil
	}

	plaintext, err := io.ReadAll(resp.Body)
	if err != nil {
		l.Error("error reading response body", "error", err)
		return fmt.Errorf("error reading response body: %w", err)
	}

	for key := range strings.SplitSeq(string(plaintext), "\n") {
		if key == "" {
			continue
		}
		pk := db.PublicKey{
			Did: did,
		}
		pk.Key = key
		if err := h.db.AddPublicKey(pk); err != nil {
			l.Error("failed to add public key", "error", err)
			return fmt.Errorf("failed to add public key: %w", err)
		}
	}
	return nil
}

func (h *Knot) processMessages(ctx context.Context, event *models.Event) error {
	if event.Kind != models.EventKindCommit {
		return nil
	}

	var err error
	defer func() {
		eventTime := event.TimeUS
		lastTimeUs := eventTime + 1
		if err := h.db.SaveLastTimeUs(lastTimeUs); err != nil {
			err = fmt.Errorf("(deferred) failed to save last time us: %w", err)
		}
	}()

	switch event.Commit.Collection {
	case tangled.PublicKeyNSID:
		err = h.processPublicKey(ctx, event)
	case tangled.KnotMemberNSID:
		err = h.processKnotMember(ctx, event)
	case tangled.RepoPullNSID:
		err = h.processPull(ctx, event)
	case tangled.RepoCollaboratorNSID:
		err = h.processCollaborator(ctx, event)
	}

	if err != nil {
		h.l.Debug("failed to process event", "nsid", event.Commit.Collection, "err", err)
	}

	return nil
}
