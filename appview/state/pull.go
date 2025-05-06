package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/auth"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/telemetry"
	"tangled.sh/tangled.sh/core/types"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"
)

// htmx fragment
func (s *State) PullActions(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "PullActions")
	defer span.End()

	switch r.Method {
	case http.MethodGet:
		user := s.auth.GetUser(r)
		f, err := s.fullyResolvedRepo(r.WithContext(ctx))
		if err != nil {
			log.Println("failed to get repo and knot", err)
			return
		}

		pull, ok := ctx.Value("pull").(*db.Pull)
		if !ok {
			log.Println("failed to get pull")
			s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
			return
		}

		roundNumberStr := chi.URLParam(r, "round")
		roundNumber, err := strconv.Atoi(roundNumberStr)
		if err != nil {
			roundNumber = pull.LastRoundNumber()
		}
		if roundNumber >= len(pull.Submissions) {
			http.Error(w, "bad round id", http.StatusBadRequest)
			log.Println("failed to parse round id", err)
			return
		}

		_, mergeSpan := s.t.TraceStart(ctx, "mergeCheck")
		mergeCheckResponse := s.mergeCheck(ctx, f, pull)
		mergeSpan.End()

		resubmitResult := pages.Unknown
		if user.Did == pull.OwnerDid {
			_, resubmitSpan := s.t.TraceStart(ctx, "resubmitCheck")
			resubmitResult = s.resubmitCheck(ctx, f, pull)
			resubmitSpan.End()
		}

		_, renderSpan := s.t.TraceStart(ctx, "renderPullActions")
		s.pages.PullActionsFragment(w, pages.PullActionsParams{
			LoggedInUser:  user,
			RepoInfo:      f.RepoInfo(ctx, s, user),
			Pull:          pull,
			RoundNumber:   roundNumber,
			MergeCheck:    mergeCheckResponse,
			ResubmitCheck: resubmitResult,
		})
		renderSpan.End()
		return
	}
}

func (s *State) RepoSinglePull(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoSinglePull")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		err := errors.New("failed to get pull from context")
		log.Println(err)
		span.RecordError(err)
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	attrs := telemetry.MapAttrs[string](map[string]string{
		"pull.id":    fmt.Sprintf("%d", pull.PullId),
		"pull.owner": pull.OwnerDid,
	})

	span.SetAttributes(attrs...)

	totalIdents := 1
	for _, submission := range pull.Submissions {
		totalIdents += len(submission.Comments)
	}

	identsToResolve := make([]string, totalIdents)

	// populate idents
	identsToResolve[0] = pull.OwnerDid
	idx := 1
	for _, submission := range pull.Submissions {
		for _, comment := range submission.Comments {
			identsToResolve[idx] = comment.OwnerDid
			idx += 1
		}
	}

	resolvedIds := s.resolver.ResolveIdents(ctx, identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}
	span.SetAttributes(attribute.Int("identities.resolved", len(resolvedIds)))

	mergeCheckResponse := s.mergeCheck(ctx, f, pull)

	resubmitResult := pages.Unknown
	if user != nil && user.Did == pull.OwnerDid {
		resubmitResult = s.resubmitCheck(ctx, f, pull)
	}

	s.pages.RepoSinglePull(w, pages.RepoSinglePullParams{
		LoggedInUser:  user,
		RepoInfo:      f.RepoInfo(ctx, s, user),
		DidHandleMap:  didHandleMap,
		Pull:          pull,
		MergeCheck:    mergeCheckResponse,
		ResubmitCheck: resubmitResult,
	})
}

func (s *State) mergeCheck(ctx context.Context, f *FullyResolvedRepo, pull *db.Pull) types.MergeCheckResponse {
	if pull.State == db.PullMerged {
		return types.MergeCheckResponse{}
	}

	secret, err := db.GetRegistrationKey(s.db, f.Knot)
	if err != nil {
		log.Printf("failed to get registration key: %v", err)
		return types.MergeCheckResponse{
			Error: "failed to check merge status: this knot is unregistered",
		}
	}

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Printf("failed to setup signed client for %s; ignoring: %v", f.Knot, err)
		return types.MergeCheckResponse{
			Error: "failed to check merge status",
		}
	}

	resp, err := ksClient.MergeCheck([]byte(pull.LatestPatch()), f.OwnerDid(), f.RepoName, pull.TargetBranch)
	if err != nil {
		log.Println("failed to check for mergeability:", err)
		return types.MergeCheckResponse{
			Error: "failed to check merge status",
		}
	}
	switch resp.StatusCode {
	case 404:
		return types.MergeCheckResponse{
			Error: "failed to check merge status: this knot does not support PRs",
		}
	case 400:
		return types.MergeCheckResponse{
			Error: "failed to check merge status: does this knot support PRs?",
		}
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("failed to read merge check response body")
		return types.MergeCheckResponse{
			Error: "failed to check merge status: knot is not speaking the right language",
		}
	}
	defer resp.Body.Close()

	var mergeCheckResponse types.MergeCheckResponse
	err = json.Unmarshal(respBody, &mergeCheckResponse)
	if err != nil {
		log.Println("failed to unmarshal merge check response", err)
		return types.MergeCheckResponse{
			Error: "failed to check merge status: knot is not speaking the right language",
		}
	}

	return mergeCheckResponse
}

func (s *State) resubmitCheck(ctx context.Context, f *FullyResolvedRepo, pull *db.Pull) pages.ResubmitResult {
	ctx, span := s.t.TraceStart(ctx, "resubmitCheck")
	defer span.End()

	span.SetAttributes(attribute.Int("pull.id", pull.PullId))

	if pull.State == db.PullMerged || pull.PullSource == nil {
		span.SetAttributes(attribute.String("result", "Unknown"))
		return pages.Unknown
	}

	var knot, ownerDid, repoName string

	if pull.PullSource.RepoAt != nil {
		// fork-based pulls
		span.SetAttributes(attribute.Bool("isForkBased", true))
		sourceRepo, err := db.GetRepoByAtUri(ctx, s.db, pull.PullSource.RepoAt.String())
		if err != nil {
			log.Println("failed to get source repo", err)
			span.RecordError(err)
			span.SetAttributes(attribute.String("error", "failed_to_get_source_repo"))
			span.SetAttributes(attribute.String("result", "Unknown"))
			return pages.Unknown
		}

		knot = sourceRepo.Knot
		ownerDid = sourceRepo.Did
		repoName = sourceRepo.Name
	} else {
		// pulls within the same repo
		span.SetAttributes(attribute.Bool("isBranchBased", true))
		knot = f.Knot
		ownerDid = f.OwnerDid()
		repoName = f.RepoName
	}

	span.SetAttributes(
		attribute.String("knot", knot),
		attribute.String("ownerDid", ownerDid),
		attribute.String("repoName", repoName),
		attribute.String("sourceBranch", pull.PullSource.Branch),
	)

	us, err := NewUnsignedClient(knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to setup client for %s; ignoring: %v", knot, err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "failed_to_setup_client"))
		span.SetAttributes(attribute.String("result", "Unknown"))
		return pages.Unknown
	}

	resp, err := us.Branch(ownerDid, repoName, pull.PullSource.Branch)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "failed_to_reach_knotserver"))
		span.SetAttributes(attribute.String("result", "Unknown"))
		return pages.Unknown
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body: %v", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "failed_to_read_response"))
		span.SetAttributes(attribute.String("result", "Unknown"))
		return pages.Unknown
	}
	defer resp.Body.Close()

	var result types.RepoBranchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		log.Println("failed to parse response:", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "failed_to_parse_response"))
		span.SetAttributes(attribute.String("result", "Unknown"))
		return pages.Unknown
	}

	latestSubmission := pull.Submissions[pull.LastRoundNumber()]

	span.SetAttributes(
		attribute.String("latestSubmission.SourceRev", latestSubmission.SourceRev),
		attribute.String("branch.Hash", result.Branch.Hash),
	)

	if latestSubmission.SourceRev != result.Branch.Hash {
		fmt.Println(latestSubmission.SourceRev, result.Branch.Hash)
		span.SetAttributes(attribute.String("result", "ShouldResubmit"))
		return pages.ShouldResubmit
	}

	span.SetAttributes(attribute.String("result", "ShouldNotResubmit"))
	return pages.ShouldNotResubmit
}

func (s *State) RepoPullPatch(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoPullPatch")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		err := errors.New("failed to get pull from context")
		log.Println(err)
		span.RecordError(err)
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	roundId := chi.URLParam(r, "round")
	roundIdInt, err := strconv.Atoi(roundId)
	if err != nil || roundIdInt >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "bad_round_id"))
		return
	}

	span.SetAttributes(
		attribute.Int("pull.id", pull.PullId),
		attribute.Int("round", roundIdInt),
		attribute.String("pull.owner", pull.OwnerDid),
	)

	identsToResolve := []string{pull.OwnerDid}
	resolvedIds := s.resolver.ResolveIdents(ctx, identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}
	span.SetAttributes(attribute.Int("identities.resolved", len(resolvedIds)))

	diff := pull.Submissions[roundIdInt].AsNiceDiff(pull.TargetBranch)

	s.pages.RepoPullPatchPage(w, pages.RepoPullPatchParams{
		LoggedInUser: user,
		DidHandleMap: didHandleMap,
		RepoInfo:     f.RepoInfo(ctx, s, user),
		Pull:         pull,
		Round:        roundIdInt,
		Submission:   pull.Submissions[roundIdInt],
		Diff:         &diff,
	})
}

func (s *State) RepoPullInterdiff(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoPullInterdiff")
	defer span.End()

	user := s.auth.GetUser(r)

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to get pull.")
		return
	}

	_, roundSpan := s.t.TraceStart(ctx, "parseRound")
	roundId := chi.URLParam(r, "round")
	roundIdInt, err := strconv.Atoi(roundId)
	if err != nil || roundIdInt >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		roundSpan.End()
		return
	}

	if roundIdInt == 0 {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("cannot interdiff initial submission")
		roundSpan.End()
		return
	}
	roundSpan.End()

	_, identSpan := s.t.TraceStart(ctx, "resolveIdentities")
	identsToResolve := []string{pull.OwnerDid}
	resolvedIds := s.resolver.ResolveIdents(ctx, identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}
	identSpan.End()

	_, diffSpan := s.t.TraceStart(ctx, "calculateInterdiff")
	currentPatch, err := pull.Submissions[roundIdInt].AsDiff(pull.TargetBranch)
	if err != nil {
		log.Println("failed to interdiff; current patch malformed")
		s.pages.Notice(w, fmt.Sprintf("interdiff-error-%d", roundIdInt), "Failed to calculate interdiff; current patch is invalid.")
		diffSpan.End()
		return
	}

	previousPatch, err := pull.Submissions[roundIdInt-1].AsDiff(pull.TargetBranch)
	if err != nil {
		log.Println("failed to interdiff; previous patch malformed")
		s.pages.Notice(w, fmt.Sprintf("interdiff-error-%d", roundIdInt), "Failed to calculate interdiff; previous patch is invalid.")
		diffSpan.End()
		return
	}

	interdiff := patchutil.Interdiff(previousPatch, currentPatch)
	diffSpan.End()

	_, renderSpan := s.t.TraceStart(ctx, "renderInterdiffPage")
	s.pages.RepoPullInterdiffPage(w, pages.RepoPullInterdiffParams{
		LoggedInUser: s.auth.GetUser(r.WithContext(ctx)),
		RepoInfo:     f.RepoInfo(ctx, s, user),
		Pull:         pull,
		Round:        roundIdInt,
		DidHandleMap: didHandleMap,
		Interdiff:    interdiff,
	})
	renderSpan.End()
	return
}

func (s *State) RepoPullPatchRaw(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoPullPatchRaw")
	defer span.End()

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	_, roundSpan := s.t.TraceStart(ctx, "parseRound")
	roundId := chi.URLParam(r, "round")
	roundIdInt, err := strconv.Atoi(roundId)
	if err != nil || roundIdInt >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		roundSpan.End()
		return
	}
	roundSpan.End()

	_, identSpan := s.t.TraceStart(ctx, "resolveIdentities")
	identsToResolve := []string{pull.OwnerDid}
	resolvedIds := s.resolver.ResolveIdents(ctx, identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}
	identSpan.End()

	_, writeSpan := s.t.TraceStart(ctx, "writePatch")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(pull.Submissions[roundIdInt].Patch))
	writeSpan.End()
}

func (s *State) RepoPulls(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoPulls")
	defer span.End()

	user := s.auth.GetUser(r)
	params := r.URL.Query()

	_, stateSpan := s.t.TraceStart(ctx, "determinePullState")
	state := db.PullOpen
	switch params.Get("state") {
	case "closed":
		state = db.PullClosed
	case "merged":
		state = db.PullMerged
	}
	stateSpan.End()

	_, repoSpan := s.t.TraceStart(ctx, "resolveRepo")
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		repoSpan.End()
		return
	}
	repoSpan.End()

	_, pullsSpan := s.t.TraceStart(ctx, "getPulls")
	pulls, err := db.GetPulls(ctx, s.db, f.RepoAt, state)
	if err != nil {
		log.Println("failed to get pulls", err)
		s.pages.Notice(w, "pulls", "Failed to load pulls. Try again later.")
		pullsSpan.End()
		return
	}
	pullsSpan.End()

	_, sourceRepoSpan := s.t.TraceStart(ctx, "resolvePullSources")
	for _, p := range pulls {
		var pullSourceRepo *db.Repo
		if p.PullSource != nil {
			if p.PullSource.RepoAt != nil {
				pullSourceRepo, err = db.GetRepoByAtUri(ctx, s.db, p.PullSource.RepoAt.String())
				if err != nil {
					log.Printf("failed to get repo by at uri: %v", err)
					continue
				} else {
					p.PullSource.Repo = pullSourceRepo
				}
			}
		}
	}
	sourceRepoSpan.End()

	_, identSpan := s.t.TraceStart(ctx, "resolveIdentities")
	identsToResolve := make([]string, len(pulls))
	for i, pull := range pulls {
		identsToResolve[i] = pull.OwnerDid
	}
	resolvedIds := s.resolver.ResolveIdents(ctx, identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}
	identSpan.End()

	_, renderSpan := s.t.TraceStart(ctx, "renderPullsPage")
	s.pages.RepoPulls(w, pages.RepoPullsParams{
		LoggedInUser: s.auth.GetUser(r.WithContext(ctx)),
		RepoInfo:     f.RepoInfo(ctx, s, user),
		Pulls:        pulls,
		DidHandleMap: didHandleMap,
		FilteringBy:  state,
	})
	renderSpan.End()
	return
}

func (s *State) PullComment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "PullComment")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	_, roundSpan := s.t.TraceStart(ctx, "parseRoundNumber")
	roundNumberStr := chi.URLParam(r, "round")
	roundNumber, err := strconv.Atoi(roundNumberStr)
	if err != nil || roundNumber >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		roundSpan.End()
		return
	}
	roundSpan.End()

	switch r.Method {
	case http.MethodGet:
		_, renderSpan := s.t.TraceStart(ctx, "renderCommentFragment")
		s.pages.PullNewCommentFragment(w, pages.PullNewCommentParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(ctx, s, user),
			Pull:         pull,
			RoundNumber:  roundNumber,
		})
		renderSpan.End()
		return
	case http.MethodPost:
		postCtx, postSpan := s.t.TraceStart(ctx, "CreateComment")
		defer postSpan.End()

		_, validateSpan := s.t.TraceStart(postCtx, "validateComment")
		body := r.FormValue("body")
		if body == "" {
			s.pages.Notice(w, "pull", "Comment body is required")
			validateSpan.End()
			return
		}
		validateSpan.End()

		// Start a transaction
		_, txSpan := s.t.TraceStart(postCtx, "startTransaction")
		tx, err := s.db.BeginTx(postCtx, nil)
		if err != nil {
			log.Println("failed to start transaction", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			txSpan.End()
			return
		}
		defer tx.Rollback()
		txSpan.End()

		createdAt := time.Now().Format(time.RFC3339)
		ownerDid := user.Did

		_, pullAtSpan := s.t.TraceStart(postCtx, "getPullAt")
		pullAt, err := db.GetPullAt(postCtx, s.db, f.RepoAt, pull.PullId)
		if err != nil {
			log.Println("failed to get pull at", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			pullAtSpan.End()
			return
		}
		pullAtSpan.End()

		_, atProtoSpan := s.t.TraceStart(postCtx, "createAtProtoRecord")
		atUri := f.RepoAt.String()
		client, _ := s.auth.AuthorizedClient(r.WithContext(postCtx))
		atResp, err := comatproto.RepoPutRecord(postCtx, client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoPullCommentNSID,
			Repo:       user.Did,
			Rkey:       appview.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoPullComment{
					Repo:      &atUri,
					Pull:      string(pullAt),
					Owner:     &ownerDid,
					Body:      body,
					CreatedAt: createdAt,
				},
			},
		})
		if err != nil {
			log.Println("failed to create pull comment", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			atProtoSpan.End()
			return
		}
		atProtoSpan.End()

		// Create the pull comment in the database with the commentAt field
		_, dbSpan := s.t.TraceStart(postCtx, "createDbComment")
		commentId, err := db.NewPullComment(postCtx, tx, &db.PullComment{
			OwnerDid:     user.Did,
			RepoAt:       f.RepoAt.String(),
			PullId:       pull.PullId,
			Body:         body,
			CommentAt:    atResp.Uri,
			SubmissionId: pull.Submissions[roundNumber].ID,
		})
		if err != nil {
			log.Println("failed to create pull comment", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			dbSpan.End()
			return
		}
		dbSpan.End()

		if err = tx.Commit(); err != nil {
			log.Println("failed to commit transaction", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d#comment-%d", f.OwnerSlashRepo(), pull.PullId, commentId))
		return
	}
}

func (s *State) NewPull(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "NewPull")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		span.SetAttributes(attribute.String("method", "GET"))

		us, err := NewUnsignedClient(f.Knot, s.config.Dev)
		if err != nil {
			log.Printf("failed to create unsigned client for %s", f.Knot)
			span.RecordError(err)
			s.pages.Error503(w)
			return
		}

		resp, err := us.Branches(f.OwnerDid(), f.RepoName)
		if err != nil {
			log.Println("failed to reach knotserver", err)
			span.RecordError(err)
			return
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error reading response body: %v", err)
			span.RecordError(err)
			return
		}

		var result types.RepoBranchesResponse
		err = json.Unmarshal(body, &result)
		if err != nil {
			log.Println("failed to parse response:", err)
			span.RecordError(err)
			return
		}

		s.pages.RepoNewPull(w, pages.RepoNewPullParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(ctx, s, user),
			Branches:     result.Branches,
		})
	case http.MethodPost:
		span.SetAttributes(attribute.String("method", "POST"))

		title := r.FormValue("title")
		body := r.FormValue("body")
		targetBranch := r.FormValue("targetBranch")
		fromFork := r.FormValue("fork")
		sourceBranch := r.FormValue("sourceBranch")
		patch := r.FormValue("patch")

		span.SetAttributes(
			attribute.String("targetBranch", targetBranch),
			attribute.String("sourceBranch", sourceBranch),
			attribute.Bool("hasFork", fromFork != ""),
			attribute.Bool("hasPatch", patch != ""),
		)

		if targetBranch == "" {
			s.pages.Notice(w, "pull", "Target branch is required.")
			span.SetAttributes(attribute.String("error", "missing_target_branch"))
			return
		}

		// Determine PR type based on input parameters
		isPushAllowed := f.RepoInfo(ctx, s, user).Roles.IsPushAllowed()
		isBranchBased := isPushAllowed && sourceBranch != "" && fromFork == ""
		isForkBased := fromFork != "" && sourceBranch != ""
		isPatchBased := patch != "" && !isBranchBased && !isForkBased

		span.SetAttributes(
			attribute.Bool("isPushAllowed", isPushAllowed),
			attribute.Bool("isBranchBased", isBranchBased),
			attribute.Bool("isForkBased", isForkBased),
			attribute.Bool("isPatchBased", isPatchBased),
		)

		if isPatchBased && !patchutil.IsFormatPatch(patch) {
			if title == "" {
				s.pages.Notice(w, "pull", "Title is required for git-diff patches.")
				span.SetAttributes(attribute.String("error", "missing_title_for_git_diff"))
				return
			}
		}

		// Validate we have at least one valid PR creation method
		if !isBranchBased && !isPatchBased && !isForkBased {
			s.pages.Notice(w, "pull", "Neither source branch nor patch supplied.")
			span.SetAttributes(attribute.String("error", "no_valid_pr_method"))
			return
		}

		// Can't mix branch-based and patch-based approaches
		if isBranchBased && patch != "" {
			s.pages.Notice(w, "pull", "Cannot select both patch and source branch.")
			span.SetAttributes(attribute.String("error", "mixed_pr_methods"))
			return
		}

		us, err := NewUnsignedClient(f.Knot, s.config.Dev)
		if err != nil {
			log.Printf("failed to create unsigned client to %s: %v", f.Knot, err)
			span.RecordError(err)
			s.pages.Notice(w, "pull", "Failed to create a pull request. Try again later.")
			return
		}

		caps, err := us.Capabilities()
		if err != nil {
			log.Println("error fetching knot caps", f.Knot, err)
			span.RecordError(err)
			s.pages.Notice(w, "pull", "Failed to create a pull request. Try again later.")
			return
		}

		span.SetAttributes(
			attribute.Bool("caps.pullRequests.formatPatch", caps.PullRequests.FormatPatch),
			attribute.Bool("caps.pullRequests.branchSubmissions", caps.PullRequests.BranchSubmissions),
			attribute.Bool("caps.pullRequests.forkSubmissions", caps.PullRequests.ForkSubmissions),
			attribute.Bool("caps.pullRequests.patchSubmissions", caps.PullRequests.PatchSubmissions),
		)

		if !caps.PullRequests.FormatPatch {
			s.pages.Notice(w, "pull", "This knot doesn't support format-patch. Unfortunately, there is no fallback for now.")
			span.SetAttributes(attribute.String("error", "formatpatch_not_supported"))
			return
		}

		// Handle the PR creation based on the type
		if isBranchBased {
			if !caps.PullRequests.BranchSubmissions {
				s.pages.Notice(w, "pull", "This knot doesn't support branch-based pull requests. Try another way?")
				span.SetAttributes(attribute.String("error", "branch_submissions_not_supported"))
				return
			}
			s.handleBranchBasedPull(w, r.WithContext(ctx), f, user, title, body, targetBranch, sourceBranch)
		} else if isForkBased {
			if !caps.PullRequests.ForkSubmissions {
				s.pages.Notice(w, "pull", "This knot doesn't support fork-based pull requests. Try another way?")
				span.SetAttributes(attribute.String("error", "fork_submissions_not_supported"))
				return
			}
			s.handleForkBasedPull(w, r.WithContext(ctx), f, user, fromFork, title, body, targetBranch, sourceBranch)
		} else if isPatchBased {
			if !caps.PullRequests.PatchSubmissions {
				s.pages.Notice(w, "pull", "This knot doesn't support patch-based pull requests. Send your patch over email.")
				span.SetAttributes(attribute.String("error", "patch_submissions_not_supported"))
				return
			}
			s.handlePatchBasedPull(w, r.WithContext(ctx), f, user, title, body, targetBranch, patch)
		}
		return
	}
}

func (s *State) handleBranchBasedPull(w http.ResponseWriter, r *http.Request, f *FullyResolvedRepo, user *auth.User, title, body, targetBranch, sourceBranch string) {
	ctx, span := s.t.TraceStart(r.Context(), "handleBranchBasedPull")
	defer span.End()

	span.SetAttributes(
		attribute.String("targetBranch", targetBranch),
		attribute.String("sourceBranch", sourceBranch),
	)

	pullSource := &db.PullSource{
		Branch: sourceBranch,
	}
	recordPullSource := &tangled.RepoPull_Source{
		Branch: sourceBranch,
	}

	// Generate a patch using /compare
	ksClient, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s: %s", f.Knot, err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "client_creation_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	comparison, err := ksClient.Compare(f.OwnerDid(), f.RepoName, targetBranch, sourceBranch)
	if err != nil {
		log.Println("failed to compare", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "comparison_failed"))
		s.pages.Notice(w, "pull", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch

	span.SetAttributes(attribute.String("sourceRev", sourceRev))

	if !patchutil.IsPatchValid(patch) {
		span.SetAttributes(attribute.String("error", "invalid_patch_format"))
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	s.createPullRequest(w, r.WithContext(ctx), f, user, title, body, targetBranch, patch, sourceRev, pullSource, recordPullSource)
}

func (s *State) handlePatchBasedPull(w http.ResponseWriter, r *http.Request, f *FullyResolvedRepo, user *auth.User, title, body, targetBranch, patch string) {
	ctx, span := s.t.TraceStart(r.Context(), "handlePatchBasedPull")
	defer span.End()

	span.SetAttributes(attribute.String("targetBranch", targetBranch))

	if !patchutil.IsPatchValid(patch) {
		span.SetAttributes(attribute.String("error", "invalid_patch_format"))
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	s.createPullRequest(w, r.WithContext(ctx), f, user, title, body, targetBranch, patch, "", nil, nil)
}

func (s *State) handleForkBasedPull(w http.ResponseWriter, r *http.Request, f *FullyResolvedRepo, user *auth.User, forkRepo string, title, body, targetBranch, sourceBranch string) {
	ctx, span := s.t.TraceStart(r.Context(), "handleForkBasedPull")
	defer span.End()

	span.SetAttributes(
		attribute.String("forkRepo", forkRepo),
		attribute.String("targetBranch", targetBranch),
		attribute.String("sourceBranch", sourceBranch),
	)

	fork, err := db.GetForkByDid(ctx, s.db, user.Did, forkRepo)
	if errors.Is(err, sql.ErrNoRows) {
		span.SetAttributes(attribute.String("error", "fork_not_found"))
		s.pages.Notice(w, "pull", "No such fork.")
		return
	} else if err != nil {
		log.Println("failed to fetch fork:", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "fork_fetch_failed"))
		s.pages.Notice(w, "pull", "Failed to fetch fork.")
		return
	}

	secret, err := db.GetRegistrationKey(s.db, fork.Knot)
	if err != nil {
		log.Println("failed to fetch registration key:", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "registration_key_fetch_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	sc, err := NewSignedClient(fork.Knot, secret, s.config.Dev)
	if err != nil {
		log.Println("failed to create signed client:", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "signed_client_creation_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	us, err := NewUnsignedClient(fork.Knot, s.config.Dev)
	if err != nil {
		log.Println("failed to create unsigned client:", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "unsigned_client_creation_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	resp, err := sc.NewHiddenRef(user.Did, fork.Name, sourceBranch, targetBranch)
	if err != nil {
		log.Println("failed to create hidden ref:", err, resp.StatusCode)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "hidden_ref_creation_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	switch resp.StatusCode {
	case 404:
		span.SetAttributes(attribute.String("error", "not_found_status"))
	case 400:
		span.SetAttributes(attribute.String("error", "bad_request_status"))
		s.pages.Notice(w, "pull", "Branch based pull requests are not supported on this knot.")
		return
	}

	hiddenRef := fmt.Sprintf("hidden/%s/%s", sourceBranch, targetBranch)
	span.SetAttributes(attribute.String("hiddenRef", hiddenRef))

	// We're now comparing the sourceBranch (on the fork) against the hiddenRef which is tracking
	// the targetBranch on the target repository. This code is a bit confusing, but here's an example:
	// hiddenRef: hidden/feature-1/main (on repo-fork)
	// targetBranch: main (on repo-1)
	// sourceBranch: feature-1 (on repo-fork)
	comparison, err := us.Compare(user.Did, fork.Name, hiddenRef, sourceBranch)
	if err != nil {
		log.Println("failed to compare across branches", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "branch_comparison_failed"))
		s.pages.Notice(w, "pull", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch
	span.SetAttributes(attribute.String("sourceRev", sourceRev))

	if !patchutil.IsPatchValid(patch) {
		span.SetAttributes(attribute.String("error", "invalid_patch_format"))
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	forkAtUri, err := syntax.ParseATURI(fork.AtUri)
	if err != nil {
		log.Println("failed to parse fork AT URI", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "fork_aturi_parse_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	s.createPullRequest(w, r.WithContext(ctx), f, user, title, body, targetBranch, patch, sourceRev, &db.PullSource{
		Branch: sourceBranch,
		RepoAt: &forkAtUri,
	}, &tangled.RepoPull_Source{Branch: sourceBranch, Repo: &fork.AtUri})
}

func (s *State) createPullRequest(
	w http.ResponseWriter,
	r *http.Request,
	f *FullyResolvedRepo,
	user *auth.User,
	title, body, targetBranch string,
	patch string,
	sourceRev string,
	pullSource *db.PullSource,
	recordPullSource *tangled.RepoPull_Source,
) {
	ctx, span := s.t.TraceStart(r.Context(), "createPullRequest")
	defer span.End()

	span.SetAttributes(
		attribute.String("targetBranch", targetBranch),
		attribute.String("sourceRev", sourceRev),
		attribute.Bool("hasPullSource", pullSource != nil),
	)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("failed to start tx")
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "transaction_start_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	// We've already checked earlier if it's diff-based and title is empty,
	// so if it's still empty now, it's intentionally skipped owing to format-patch.
	if title == "" {
		formatPatches, err := patchutil.ExtractPatches(patch)
		if err != nil {
			span.RecordError(err)
			span.SetAttributes(attribute.String("error", "extract_patches_failed"))
			s.pages.Notice(w, "pull", fmt.Sprintf("Failed to extract patches: %v", err))
			return
		}
		if len(formatPatches) == 0 {
			span.SetAttributes(attribute.String("error", "no_patches_found"))
			s.pages.Notice(w, "pull", "No patches found in the supplied format-patch.")
			return
		}

		title = formatPatches[0].Title
		body = formatPatches[0].Body
		span.SetAttributes(
			attribute.Bool("title_extracted", true),
			attribute.Bool("body_extracted", formatPatches[0].Body != ""),
		)
	}

	rkey := appview.TID()
	initialSubmission := db.PullSubmission{
		Patch:     patch,
		SourceRev: sourceRev,
	}
	err = db.NewPull(ctx, tx, &db.Pull{
		Title:        title,
		Body:         body,
		TargetBranch: targetBranch,
		OwnerDid:     user.Did,
		RepoAt:       f.RepoAt,
		Rkey:         rkey,
		Submissions: []*db.PullSubmission{
			&initialSubmission,
		},
		PullSource: pullSource,
	})
	if err != nil {
		log.Println("failed to create pull request", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "db_create_pull_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	client, _ := s.auth.AuthorizedClient(r.WithContext(ctx))
	pullId, err := db.NextPullId(s.db, f.RepoAt)
	if err != nil {
		log.Println("failed to get pull id", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "get_pull_id_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}
	span.SetAttributes(attribute.Int("pullId", pullId))

	_, err = comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoPullNSID,
		Repo:       user.Did,
		Rkey:       rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoPull{
				Title:        title,
				PullId:       int64(pullId),
				TargetRepo:   string(f.RepoAt),
				TargetBranch: targetBranch,
				Patch:        patch,
				Source:       recordPullSource,
			},
		},
	})

	if err != nil {
		log.Println("failed to create pull request", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "atproto_create_record_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "transaction_commit_failed"))
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pullId))
}

func (s *State) ValidatePatch(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "ValidatePatch")
	defer span.End()

	_, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "resolve_repo_failed"))
		return
	}

	patch := r.FormValue("patch")
	span.SetAttributes(attribute.Bool("hasPatch", patch != ""))

	if patch == "" {
		span.SetAttributes(attribute.String("error", "empty_patch"))
		s.pages.Notice(w, "patch-error", "Patch is required.")
		return
	}

	if !patchutil.IsPatchValid(patch) {
		span.SetAttributes(attribute.String("error", "invalid_patch_format"))
		s.pages.Notice(w, "patch-error", "Invalid patch format. Please provide a valid git diff or format-patch.")
		return
	}

	isFormatPatch := patchutil.IsFormatPatch(patch)
	span.SetAttributes(attribute.Bool("isFormatPatch", isFormatPatch))

	if isFormatPatch {
		s.pages.Notice(w, "patch-preview", "git-format-patch detected. Title and description are optional; if left out, they will be extracted from the first commit.")
	} else {
		s.pages.Notice(w, "patch-preview", "Regular git-diff detected. Please provide a title and description.")
	}
}

func (s *State) PatchUploadFragment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "PatchUploadFragment")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "resolve_repo_failed"))
		return
	}

	s.pages.PullPatchUploadFragment(w, pages.PullPatchUploadParams{
		RepoInfo: f.RepoInfo(ctx, s, user),
	})
}

func (s *State) CompareBranchesFragment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "CompareBranchesFragment")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "resolve_repo_failed"))
		return
	}

	us, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", f.Knot)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "client_creation_failed"))
		s.pages.Error503(w)
		return
	}

	resp, err := us.Branches(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "knotserver_connection_failed"))
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "response_read_failed"))
		return
	}
	defer resp.Body.Close()

	var result types.RepoBranchesResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "response_parse_failed"))
		return
	}
	span.SetAttributes(attribute.Int("branches.count", len(result.Branches)))

	s.pages.PullCompareBranchesFragment(w, pages.PullCompareBranchesParams{
		RepoInfo: f.RepoInfo(ctx, s, user),
		Branches: result.Branches,
	})
}

func (s *State) CompareForksFragment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "CompareForksFragment")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	forks, err := db.GetForksByDid(ctx, s.db, user.Did)
	if err != nil {
		log.Println("failed to get forks", err)
		span.RecordError(err)
		return
	}

	s.pages.PullCompareForkFragment(w, pages.PullCompareForkParams{
		RepoInfo: f.RepoInfo(ctx, s, user),
		Forks:    forks,
	})
}

func (s *State) CompareForksBranchesFragment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "CompareForksBranchesFragment")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	forkVal := r.URL.Query().Get("fork")
	span.SetAttributes(attribute.String("fork", forkVal))

	// fork repo
	repo, err := db.GetRepo(ctx, s.db, user.Did, forkVal)
	if err != nil {
		log.Println("failed to get repo", user.Did, forkVal)
		span.RecordError(err)
		return
	}

	sourceBranchesClient, err := NewUnsignedClient(repo.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", repo.Knot)
		span.RecordError(err)
		s.pages.Error503(w)
		return
	}

	sourceResp, err := sourceBranchesClient.Branches(user.Did, repo.Name)
	if err != nil {
		log.Println("failed to reach knotserver for source branches", err)
		span.RecordError(err)
		return
	}

	sourceBody, err := io.ReadAll(sourceResp.Body)
	if err != nil {
		log.Println("failed to read source response body", err)
		span.RecordError(err)
		return
	}
	defer sourceResp.Body.Close()

	var sourceResult types.RepoBranchesResponse
	err = json.Unmarshal(sourceBody, &sourceResult)
	if err != nil {
		log.Println("failed to parse source branches response:", err)
		span.RecordError(err)
		return
	}

	targetBranchesClient, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for target knot %s", f.Knot)
		span.RecordError(err)
		s.pages.Error503(w)
		return
	}

	targetResp, err := targetBranchesClient.Branches(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver for target branches", err)
		span.RecordError(err)
		return
	}

	targetBody, err := io.ReadAll(targetResp.Body)
	if err != nil {
		log.Println("failed to read target response body", err)
		span.RecordError(err)
		return
	}
	defer targetResp.Body.Close()

	var targetResult types.RepoBranchesResponse
	err = json.Unmarshal(targetBody, &targetResult)
	if err != nil {
		log.Println("failed to parse target branches response:", err)
		span.RecordError(err)
		return
	}

	s.pages.PullCompareForkBranchesFragment(w, pages.PullCompareForkBranchesParams{
		RepoInfo:       f.RepoInfo(ctx, s, user),
		SourceBranches: sourceResult.Branches,
		TargetBranches: targetResult.Branches,
	})
}

func (s *State) ResubmitPull(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "ResubmitPull")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		span.RecordError(errors.New("failed to get pull from context"))
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.Int("pull.id", pull.PullId),
		attribute.String("pull.owner", pull.OwnerDid),
		attribute.String("method", r.Method),
	)

	switch r.Method {
	case http.MethodGet:
		s.pages.PullResubmitFragment(w, pages.PullResubmitParams{
			RepoInfo: f.RepoInfo(ctx, s, user),
			Pull:     pull,
		})
		return
	case http.MethodPost:
		if pull.IsPatchBased() {
			span.SetAttributes(attribute.String("pull.type", "patch_based"))
			s.resubmitPatch(w, r.WithContext(ctx))
			return
		} else if pull.IsBranchBased() {
			span.SetAttributes(attribute.String("pull.type", "branch_based"))
			s.resubmitBranch(w, r.WithContext(ctx))
			return
		} else if pull.IsForkBased() {
			span.SetAttributes(attribute.String("pull.type", "fork_based"))
			s.resubmitFork(w, r.WithContext(ctx))
			return
		}
		span.SetAttributes(attribute.String("pull.type", "unknown"))
	}
}

func (s *State) resubmitPatch(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "resubmitPatch")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		span.RecordError(errors.New("failed to get pull from context"))
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.Int("pull.id", pull.PullId),
		attribute.String("pull.owner", pull.OwnerDid),
	)

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	if user.Did != pull.OwnerDid {
		log.Println("unauthorized user")
		span.SetAttributes(attribute.String("error", "unauthorized_user"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	patch := r.FormValue("patch")
	span.SetAttributes(attribute.Bool("has_patch", patch != ""))

	if err = validateResubmittedPatch(pull, patch); err != nil {
		span.SetAttributes(attribute.String("error", "invalid_patch"))
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("failed to start tx")
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	err = db.ResubmitPull(tx, pull, patch, "")
	if err != nil {
		log.Println("failed to resubmit pull request", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to resubmit pull request. Try again later.")
		return
	}
	client, _ := s.auth.AuthorizedClient(r.WithContext(ctx))

	ex, err := comatproto.RepoGetRecord(ctx, client, "", tangled.RepoPullNSID, user.Did, pull.Rkey)
	if err != nil {
		// failed to get record
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "record_not_found"))
		s.pages.Notice(w, "resubmit-error", "Failed to update pull, no record found on PDS.")
		return
	}

	_, err = comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoPullNSID,
		Repo:       user.Did,
		Rkey:       pull.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoPull{
				Title:        pull.Title,
				PullId:       int64(pull.PullId),
				TargetRepo:   string(f.RepoAt),
				TargetBranch: pull.TargetBranch,
				Patch:        patch, // new patch
			},
		},
	})
	if err != nil {
		log.Println("failed to update record", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to update pull request on the PDS. Try again later.")
		return
	}

	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to resubmit pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}

func (s *State) resubmitBranch(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "resubmitBranch")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		span.RecordError(errors.New("failed to get pull from context"))
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.Int("pull.id", pull.PullId),
		attribute.String("pull.owner", pull.OwnerDid),
		attribute.String("pull.source_branch", pull.PullSource.Branch),
		attribute.String("pull.target_branch", pull.TargetBranch),
	)

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	if user.Did != pull.OwnerDid {
		log.Println("unauthorized user")
		span.SetAttributes(attribute.String("error", "unauthorized_user"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if !f.RepoInfo(ctx, s, user).Roles.IsPushAllowed() {
		log.Println("unauthorized user")
		span.SetAttributes(attribute.String("error", "push_not_allowed"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	ksClient, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create client for %s: %s", f.Knot, err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "client_creation_failed"))
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	comparison, err := ksClient.Compare(f.OwnerDid(), f.RepoName, pull.TargetBranch, pull.PullSource.Branch)
	if err != nil {
		log.Printf("compare request failed: %s", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "compare_failed"))
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch
	span.SetAttributes(attribute.String("source_rev", sourceRev))

	if err = validateResubmittedPatch(pull, patch); err != nil {
		span.SetAttributes(attribute.String("error", "invalid_patch"))
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	if sourceRev == pull.Submissions[pull.LastRoundNumber()].SourceRev {
		span.SetAttributes(attribute.String("error", "no_changes"))
		s.pages.Notice(w, "resubmit-error", "This branch has not changed since the last submission.")
		return
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("failed to start tx")
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	err = db.ResubmitPull(tx, pull, patch, sourceRev)
	if err != nil {
		log.Println("failed to create pull request", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	client, _ := s.auth.AuthorizedClient(r.WithContext(ctx))

	ex, err := comatproto.RepoGetRecord(ctx, client, "", tangled.RepoPullNSID, user.Did, pull.Rkey)
	if err != nil {
		// failed to get record
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "record_not_found"))
		s.pages.Notice(w, "resubmit-error", "Failed to update pull, no record found on PDS.")
		return
	}

	recordPullSource := &tangled.RepoPull_Source{
		Branch: pull.PullSource.Branch,
	}
	_, err = comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoPullNSID,
		Repo:       user.Did,
		Rkey:       pull.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoPull{
				Title:        pull.Title,
				PullId:       int64(pull.PullId),
				TargetRepo:   string(f.RepoAt),
				TargetBranch: pull.TargetBranch,
				Patch:        patch, // new patch
				Source:       recordPullSource,
			},
		},
	})
	if err != nil {
		log.Println("failed to update record", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to update pull request on the PDS. Try again later.")
		return
	}

	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to resubmit pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}

func (s *State) resubmitFork(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "resubmitFork")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		span.RecordError(errors.New("failed to get pull from context"))
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.Int("pull.id", pull.PullId),
		attribute.String("pull.owner", pull.OwnerDid),
		attribute.String("pull.source_branch", pull.PullSource.Branch),
		attribute.String("pull.target_branch", pull.TargetBranch),
	)

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		return
	}

	if user.Did != pull.OwnerDid {
		log.Println("unauthorized user")
		span.SetAttributes(attribute.String("error", "unauthorized_user"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	forkRepo, err := db.GetRepoByAtUri(ctx, s.db, pull.PullSource.RepoAt.String())
	if err != nil {
		log.Println("failed to get source repo", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "source_repo_not_found"))
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.String("fork.knot", forkRepo.Knot),
		attribute.String("fork.did", forkRepo.Did),
		attribute.String("fork.name", forkRepo.Name),
	)

	// extract patch by performing compare
	ksClient, err := NewUnsignedClient(forkRepo.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create client for %s: %s", forkRepo.Knot, err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "client_creation_failed"))
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	secret, err := db.GetRegistrationKey(s.db, forkRepo.Knot)
	if err != nil {
		log.Printf("failed to get registration key for %s: %s", forkRepo.Knot, err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "reg_key_not_found"))
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	// update the hidden tracking branch to latest
	signedClient, err := NewSignedClient(forkRepo.Knot, secret, s.config.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s: %s", forkRepo.Knot, err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "signed_client_creation_failed"))
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	resp, err := signedClient.NewHiddenRef(forkRepo.Did, forkRepo.Name, pull.PullSource.Branch, pull.TargetBranch)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		log.Printf("failed to update tracking branch: %s", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "hidden_ref_update_failed"))
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	hiddenRef := fmt.Sprintf("hidden/%s/%s", pull.PullSource.Branch, pull.TargetBranch)
	span.SetAttributes(attribute.String("hidden_ref", hiddenRef))

	comparison, err := ksClient.Compare(forkRepo.Did, forkRepo.Name, hiddenRef, pull.PullSource.Branch)
	if err != nil {
		log.Printf("failed to compare branches: %s", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "compare_failed"))
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch
	span.SetAttributes(attribute.String("source_rev", sourceRev))

	if err = validateResubmittedPatch(pull, patch); err != nil {
		span.SetAttributes(attribute.String("error", "invalid_patch"))
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	if sourceRev == pull.Submissions[pull.LastRoundNumber()].SourceRev {
		span.SetAttributes(attribute.String("error", "no_changes"))
		s.pages.Notice(w, "resubmit-error", "This branch has not changed since the last submission.")
		return
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("failed to start tx")
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	err = db.ResubmitPull(tx, pull, patch, sourceRev)
	if err != nil {
		log.Println("failed to create pull request", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	client, _ := s.auth.AuthorizedClient(r.WithContext(ctx))

	ex, err := comatproto.RepoGetRecord(ctx, client, "", tangled.RepoPullNSID, user.Did, pull.Rkey)
	if err != nil {
		// failed to get record
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "record_not_found"))
		s.pages.Notice(w, "resubmit-error", "Failed to update pull, no record found on PDS.")
		return
	}

	repoAt := pull.PullSource.RepoAt.String()
	recordPullSource := &tangled.RepoPull_Source{
		Branch: pull.PullSource.Branch,
		Repo:   &repoAt,
	}
	_, err = comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoPullNSID,
		Repo:       user.Did,
		Rkey:       pull.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoPull{
				Title:        pull.Title,
				PullId:       int64(pull.PullId),
				TargetRepo:   string(f.RepoAt),
				TargetBranch: pull.TargetBranch,
				Patch:        patch, // new patch
				Source:       recordPullSource,
			},
		},
	})
	if err != nil {
		log.Println("failed to update record", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to update pull request on the PDS. Try again later.")
		return
	}

	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		span.RecordError(err)
		s.pages.Notice(w, "resubmit-error", "Failed to resubmit pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}

// validate a resubmission against a pull request
func validateResubmittedPatch(pull *db.Pull, patch string) error {
	if patch == "" {
		return fmt.Errorf("Patch is empty.")
	}

	if patch == pull.LatestPatch() {
		return fmt.Errorf("Patch is identical to previous submission.")
	}

	if !patchutil.IsPatchValid(patch) {
		return fmt.Errorf("Invalid patch format. Please provide a valid diff.")
	}

	return nil
}

func (s *State) MergePull(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "MergePull")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to resolve repo:", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "resolve_repo_failed"))
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		span.SetAttributes(attribute.String("error", "pull_not_in_context"))
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.Int("pull.id", pull.PullId),
		attribute.String("pull.owner", pull.OwnerDid),
		attribute.String("target_branch", pull.TargetBranch),
	)

	secret, err := db.GetRegistrationKey(s.db, f.Knot)
	if err != nil {
		log.Printf("no registration key found for domain %s: %s\n", f.Knot, err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "reg_key_not_found"))
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	ident, err := s.resolver.ResolveIdent(ctx, pull.OwnerDid)
	if err != nil {
		log.Printf("resolving identity: %s", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "resolve_identity_failed"))
		w.WriteHeader(http.StatusNotFound)
		return
	}

	email, err := db.GetPrimaryEmail(s.db, pull.OwnerDid)
	if err != nil {
		log.Printf("failed to get primary email: %s", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "get_email_failed"))
	}

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s: %s", f.Knot, err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "client_creation_failed"))
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	// Merge the pull request
	resp, err := ksClient.Merge([]byte(pull.LatestPatch()), f.OwnerDid(), f.RepoName, pull.TargetBranch, pull.Title, pull.Body, ident.Handle.String(), email.Address)
	if err != nil {
		log.Printf("failed to merge pull request: %s", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "merge_failed"))
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	span.SetAttributes(attribute.Int("response.status", resp.StatusCode))

	if resp.StatusCode == http.StatusOK {
		err := db.MergePull(s.db, f.RepoAt, pull.PullId)
		if err != nil {
			log.Printf("failed to update pull request status in database: %s", err)
			span.RecordError(err)
			span.SetAttributes(attribute.String("error", "db_update_failed"))
			s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
			return
		}
		s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s/pulls/%d", f.OwnerHandle(), f.RepoName, pull.PullId))
	} else {
		log.Printf("knotserver returned non-OK status code for merge: %d", resp.StatusCode)
		span.SetAttributes(attribute.String("error", "non_ok_response"))
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
	}
}

func (s *State) ClosePull(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "ClosePull")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("malformed middleware")
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "resolve_repo_failed"))
		return
	}

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		span.SetAttributes(attribute.String("error", "pull_not_in_context"))
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.Int("pull.id", pull.PullId),
		attribute.String("pull.owner", pull.OwnerDid),
		attribute.String("user.did", user.Did),
	)

	// auth filter: only owner or collaborators can close
	roles := RolesInRepo(s, user, f)
	isCollaborator := roles.IsCollaborator()
	isPullAuthor := user.Did == pull.OwnerDid
	isCloseAllowed := isCollaborator || isPullAuthor

	span.SetAttributes(
		attribute.Bool("is_collaborator", isCollaborator),
		attribute.Bool("is_pull_author", isPullAuthor),
		attribute.Bool("is_close_allowed", isCloseAllowed),
	)

	if !isCloseAllowed {
		log.Println("failed to close pull")
		span.SetAttributes(attribute.String("error", "unauthorized"))
		s.pages.Notice(w, "pull-close", "You are unauthorized to close this pull.")
		return
	}

	// Start a transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("failed to start transaction", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "transaction_start_failed"))
		s.pages.Notice(w, "pull-close", "Failed to close pull.")
		return
	}

	// Close the pull in the database
	err = db.ClosePull(tx, f.RepoAt, pull.PullId)
	if err != nil {
		log.Println("failed to close pull", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "db_close_failed"))
		s.pages.Notice(w, "pull-close", "Failed to close pull.")
		return
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "transaction_commit_failed"))
		s.pages.Notice(w, "pull-close", "Failed to close pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}

func (s *State) ReopenPull(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "ReopenPull")
	defer span.End()

	user := s.auth.GetUser(r.WithContext(ctx))

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to resolve repo", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "resolve_repo_failed"))
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	pull, ok := ctx.Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		span.SetAttributes(attribute.String("error", "pull_not_in_context"))
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.Int("pull.id", pull.PullId),
		attribute.String("pull.owner", pull.OwnerDid),
		attribute.String("user.did", user.Did),
	)

	// auth filter: only owner or collaborators can reopen
	roles := RolesInRepo(s, user, f)
	isCollaborator := roles.IsCollaborator()
	isPullAuthor := user.Did == pull.OwnerDid
	isReopenAllowed := isCollaborator || isPullAuthor

	span.SetAttributes(
		attribute.Bool("is_collaborator", isCollaborator),
		attribute.Bool("is_pull_author", isPullAuthor),
		attribute.Bool("is_reopen_allowed", isReopenAllowed),
	)

	if !isReopenAllowed {
		log.Println("failed to reopen pull")
		span.SetAttributes(attribute.String("error", "unauthorized"))
		s.pages.Notice(w, "pull-close", "You are unauthorized to reopen this pull.")
		return
	}

	// Start a transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("failed to start transaction", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "transaction_start_failed"))
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	// Reopen the pull in the database
	err = db.ReopenPull(tx, f.RepoAt, pull.PullId)
	if err != nil {
		log.Println("failed to reopen pull", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "db_reopen_failed"))
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error", "transaction_commit_failed"))
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}
