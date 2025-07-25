package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/bluesky-social/jetstream/pkg/models"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/knotserver/git"
	"tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/workflow"
)

func (h *Handle) processPublicKey(ctx context.Context, did string, record tangled.PublicKey) error {
	l := log.FromContext(ctx)
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

func (h *Handle) processKnotMember(ctx context.Context, did string, record tangled.KnotMember) error {
	l := log.FromContext(ctx)

	if record.Domain != h.c.Server.Hostname {
		l.Error("domain mismatch", "domain", record.Domain, "expected", h.c.Server.Hostname)
		return fmt.Errorf("domain mismatch: %s != %s", record.Domain, h.c.Server.Hostname)
	}

	ok, err := h.e.E.Enforce(did, ThisServer, ThisServer, "server:invite")
	if err != nil || !ok {
		l.Error("failed to add member", "did", did)
		return fmt.Errorf("failed to enforce permissions: %w", err)
	}

	if err := h.e.AddKnotMember(ThisServer, record.Subject); err != nil {
		l.Error("failed to add member", "error", err)
		return fmt.Errorf("failed to add member: %w", err)
	}
	l.Info("added member from firehose", "member", record.Subject)

	if err := h.db.AddDid(did); err != nil {
		l.Error("failed to add did", "error", err)
		return fmt.Errorf("failed to add did: %w", err)
	}
	h.jc.AddDid(did)

	if err := h.fetchAndAddKeys(ctx, did); err != nil {
		return fmt.Errorf("failed to fetch and add keys: %w", err)
	}

	return nil
}

func (h *Handle) processPull(ctx context.Context, did string, record tangled.RepoPull) error {
	l := log.FromContext(ctx)
	l = l.With("handler", "processPull")
	l = l.With("did", did)
	l = l.With("target_repo", record.TargetRepo)
	l = l.With("target_branch", record.TargetBranch)

	if record.Source == nil {
		reason := "not a branch-based pull request"
		l.Info("ignoring pull record", "reason", reason)
		return fmt.Errorf("ignoring pull record: %s", reason)
	}

	if record.Source.Repo != nil {
		reason := "fork based pull"
		l.Info("ignoring pull record", "reason", reason)
		return fmt.Errorf("ignoring pull record: %s", reason)
	}

	allDids, err := h.db.GetAllDids()
	if err != nil {
		return err
	}

	// presently: we only process PRs from collaborators for pipelines
	if !slices.Contains(allDids, did) {
		reason := "not a known did"
		l.Info("rejecting pull record", "reason", reason)
		return fmt.Errorf("rejected pull record: %s, %s", reason, did)
	}

	repoAt, err := syntax.ParseATURI(record.TargetRepo)
	if err != nil {
		return err
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
		return err
	}

	repo := resp.Value.Val.(*tangled.Repo)

	if repo.Knot != h.c.Server.Hostname {
		reason := "not this knot"
		l.Info("rejecting pull record", "reason", reason)
		return fmt.Errorf("rejected pull record: %s", reason)
	}

	didSlashRepo, err := securejoin.SecureJoin(repo.Owner, repo.Name)
	if err != nil {
		return err
	}

	repoPath, err := securejoin.SecureJoin(h.c.Repo.ScanPath, didSlashRepo)
	if err != nil {
		return err
	}

	gr, err := git.Open(repoPath, record.Source.Branch)
	if err != nil {
		return err
	}

	workflowDir, err := gr.FileTree(ctx, workflow.WorkflowDir)
	if err != nil {
		return err
	}

	var pipeline workflow.Pipeline
	for _, e := range workflowDir {
		if !e.IsFile {
			continue
		}

		fpath := filepath.Join(workflow.WorkflowDir, e.Name)
		contents, err := gr.RawContent(fpath)
		if err != nil {
			continue
		}

		wf, err := workflow.FromFile(e.Name, contents)
		if err != nil {
			// TODO: log here, respond to client that is pushing
			h.l.Error("failed to parse workflow", "err", err, "path", fpath)
			continue
		}

		pipeline = append(pipeline, wf)
	}

	trigger := tangled.Pipeline_PullRequestTriggerData{
		Action:       "create",
		SourceBranch: record.Source.Branch,
		SourceSha:    record.Source.Sha,
		TargetBranch: record.TargetBranch,
	}

	compiler := workflow.Compiler{
		Trigger: tangled.Pipeline_TriggerMetadata{
			Kind:        string(workflow.TriggerKindPullRequest),
			PullRequest: &trigger,
			Repo: &tangled.Pipeline_TriggerRepo{
				Did:  repo.Owner,
				Knot: repo.Knot,
				Repo: repo.Name,
			},
		},
	}

	cp := compiler.Compile(pipeline)
	eventJson, err := json.Marshal(cp)
	if err != nil {
		return err
	}

	// do not run empty pipelines
	if cp.Workflows == nil {
		return nil
	}

	event := db.Event{
		Rkey:      TID(),
		Nsid:      tangled.PipelineNSID,
		EventJson: string(eventJson),
	}

	return h.db.InsertEvent(event, h.n)
}

func (h *Handle) fetchAndAddKeys(ctx context.Context, did string) error {
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

	for _, key := range strings.Split(string(plaintext), "\n") {
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

func (h *Handle) processMessages(ctx context.Context, event *models.Event) error {
	did := event.Did
	if event.Kind != models.EventKindCommit {
		return nil
	}

	var err error
	defer func() {
		eventTime := event.TimeUS
		lastTimeUs := eventTime + 1
		fmt.Println("lastTimeUs", lastTimeUs)
		if err := h.db.SaveLastTimeUs(lastTimeUs); err != nil {
			err = fmt.Errorf("(deferred) failed to save last time us: %w", err)
		}
	}()

	raw := json.RawMessage(event.Commit.Record)

	switch event.Commit.Collection {
	case tangled.PublicKeyNSID:
		var record tangled.PublicKey
		if err := json.Unmarshal(raw, &record); err != nil {
			return fmt.Errorf("failed to unmarshal record: %w", err)
		}
		if err := h.processPublicKey(ctx, did, record); err != nil {
			return fmt.Errorf("failed to process public key: %w", err)
		}

	case tangled.KnotMemberNSID:
		var record tangled.KnotMember
		if err := json.Unmarshal(raw, &record); err != nil {
			return fmt.Errorf("failed to unmarshal record: %w", err)
		}
		if err := h.processKnotMember(ctx, did, record); err != nil {
			return fmt.Errorf("failed to process knot member: %w", err)
		}
	case tangled.RepoPullNSID:
		var record tangled.RepoPull
		if err := json.Unmarshal(raw, &record); err != nil {
			return fmt.Errorf("failed to unmarshal record: %w", err)
		}
		if err := h.processPull(ctx, did, record); err != nil {
			return fmt.Errorf("failed to process knot member: %w", err)
		}
	}

	return err
}
