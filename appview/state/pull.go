package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/auth"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/types"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"
)

// htmx fragment
func (s *State) PullActions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user := s.auth.GetUser(r)
		f, err := fullyResolvedRepo(r)
		if err != nil {
			log.Println("failed to get repo and knot", err)
			return
		}

		pull, ok := r.Context().Value("pull").(*db.Pull)
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

		mergeCheckResponse := s.mergeCheck(f, pull)
		resubmitResult := pages.Unknown
		if user.Did == pull.OwnerDid {
			resubmitResult = s.resubmitCheck(f, pull)
		}

		s.pages.PullActionsFragment(w, pages.PullActionsParams{
			LoggedInUser:  user,
			RepoInfo:      f.RepoInfo(s, user),
			Pull:          pull,
			RoundNumber:   roundNumber,
			MergeCheck:    mergeCheckResponse,
			ResubmitCheck: resubmitResult,
		})
		return
	}
}

func (s *State) RepoSinglePull(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

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

	resolvedIds := s.resolver.ResolveIdents(r.Context(), identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	mergeCheckResponse := s.mergeCheck(f, pull)
	resubmitResult := pages.Unknown
	if user != nil && user.Did == pull.OwnerDid {
		resubmitResult = s.resubmitCheck(f, pull)
	}

	s.pages.RepoSinglePull(w, pages.RepoSinglePullParams{
		LoggedInUser:  user,
		RepoInfo:      f.RepoInfo(s, user),
		DidHandleMap:  didHandleMap,
		Pull:          pull,
		MergeCheck:    mergeCheckResponse,
		ResubmitCheck: resubmitResult,
	})
}

func (s *State) mergeCheck(f *FullyResolvedRepo, pull *db.Pull) types.MergeCheckResponse {
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

func (s *State) resubmitCheck(f *FullyResolvedRepo, pull *db.Pull) pages.ResubmitResult {
	if pull.State == db.PullMerged || pull.PullSource == nil {
		return pages.Unknown
	}

	var knot, ownerDid, repoName string

	if pull.PullSource.RepoAt != nil {
		// fork-based pulls
		sourceRepo, err := db.GetRepoByAtUri(s.db, pull.PullSource.RepoAt.String())
		if err != nil {
			log.Println("failed to get source repo", err)
			return pages.Unknown
		}

		knot = sourceRepo.Knot
		ownerDid = sourceRepo.Did
		repoName = sourceRepo.Name
	} else {
		// pulls within the same repo
		knot = f.Knot
		ownerDid = f.OwnerDid()
		repoName = f.RepoName
	}

	us, err := NewUnsignedClient(knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to setup client for %s; ignoring: %v", knot, err)
		return pages.Unknown
	}

	resp, err := us.Branch(ownerDid, repoName, pull.PullSource.Branch)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return pages.Unknown
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body: %v", err)
		return pages.Unknown
	}
	defer resp.Body.Close()

	var result types.RepoBranchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		log.Println("failed to parse response:", err)
		return pages.Unknown
	}

	latestSubmission := pull.Submissions[pull.LastRoundNumber()]
	if latestSubmission.SourceRev != result.Branch.Hash {
		fmt.Println(latestSubmission.SourceRev, result.Branch.Hash)
		return pages.ShouldResubmit
	}

	return pages.ShouldNotResubmit
}

func (s *State) RepoPullPatch(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	roundId := chi.URLParam(r, "round")
	roundIdInt, err := strconv.Atoi(roundId)
	if err != nil || roundIdInt >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		return
	}

	identsToResolve := []string{pull.OwnerDid}
	resolvedIds := s.resolver.ResolveIdents(r.Context(), identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	diff := pull.Submissions[roundIdInt].AsNiceDiff(pull.TargetBranch)

	s.pages.RepoPullPatchPage(w, pages.RepoPullPatchParams{
		LoggedInUser: user,
		DidHandleMap: didHandleMap,
		RepoInfo:     f.RepoInfo(s, user),
		Pull:         pull,
		Round:        roundIdInt,
		Submission:   pull.Submissions[roundIdInt],
		Diff:         &diff,
	})

}

func (s *State) RepoPullInterdiff(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to get pull.")
		return
	}

	roundId := chi.URLParam(r, "round")
	roundIdInt, err := strconv.Atoi(roundId)
	if err != nil || roundIdInt >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		return
	}

	if roundIdInt == 0 {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("cannot interdiff initial submission")
		return
	}

	identsToResolve := []string{pull.OwnerDid}
	resolvedIds := s.resolver.ResolveIdents(r.Context(), identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	currentPatch, err := pull.Submissions[roundIdInt].AsDiff(pull.TargetBranch)
	if err != nil {
		log.Println("failed to interdiff; current patch malformed")
		s.pages.Notice(w, fmt.Sprintf("interdiff-error-%d", roundIdInt), "Failed to calculate interdiff; current patch is invalid.")
		return
	}

	previousPatch, err := pull.Submissions[roundIdInt-1].AsDiff(pull.TargetBranch)
	if err != nil {
		log.Println("failed to interdiff; previous patch malformed")
		s.pages.Notice(w, fmt.Sprintf("interdiff-error-%d", roundIdInt), "Failed to calculate interdiff; previous patch is invalid.")
		return
	}

	interdiff := patchutil.Interdiff(previousPatch, currentPatch)

	s.pages.RepoPullInterdiffPage(w, pages.RepoPullInterdiffParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo:     f.RepoInfo(s, user),
		Pull:         pull,
		Round:        roundIdInt,
		DidHandleMap: didHandleMap,
		Interdiff:    interdiff,
	})
	return
}

func (s *State) RepoPullPatchRaw(w http.ResponseWriter, r *http.Request) {
	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	roundId := chi.URLParam(r, "round")
	roundIdInt, err := strconv.Atoi(roundId)
	if err != nil || roundIdInt >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		return
	}

	identsToResolve := []string{pull.OwnerDid}
	resolvedIds := s.resolver.ResolveIdents(r.Context(), identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(pull.Submissions[roundIdInt].Patch))
}

func (s *State) RepoPulls(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	params := r.URL.Query()

	state := db.PullOpen
	switch params.Get("state") {
	case "closed":
		state = db.PullClosed
	case "merged":
		state = db.PullMerged
	}

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pulls, err := db.GetPulls(s.db, f.RepoAt, state)
	if err != nil {
		log.Println("failed to get pulls", err)
		s.pages.Notice(w, "pulls", "Failed to load pulls. Try again later.")
		return
	}

	for _, p := range pulls {
		var pullSourceRepo *db.Repo
		if p.PullSource != nil {
			if p.PullSource.RepoAt != nil {
				pullSourceRepo, err = db.GetRepoByAtUri(s.db, p.PullSource.RepoAt.String())
				if err != nil {
					log.Printf("failed to get repo by at uri: %v", err)
					continue
				} else {
					p.PullSource.Repo = pullSourceRepo
				}
			}
		}
	}

	identsToResolve := make([]string, len(pulls))
	for i, pull := range pulls {
		identsToResolve[i] = pull.OwnerDid
	}
	resolvedIds := s.resolver.ResolveIdents(r.Context(), identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	s.pages.RepoPulls(w, pages.RepoPullsParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo:     f.RepoInfo(s, user),
		Pulls:        pulls,
		DidHandleMap: didHandleMap,
		FilteringBy:  state,
	})
	return
}

func (s *State) PullComment(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	roundNumberStr := chi.URLParam(r, "round")
	roundNumber, err := strconv.Atoi(roundNumberStr)
	if err != nil || roundNumber >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.pages.PullNewCommentFragment(w, pages.PullNewCommentParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(s, user),
			Pull:         pull,
			RoundNumber:  roundNumber,
		})
		return
	case http.MethodPost:
		body := r.FormValue("body")
		if body == "" {
			s.pages.Notice(w, "pull", "Comment body is required")
			return
		}

		// Start a transaction
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Println("failed to start transaction", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			return
		}
		defer tx.Rollback()

		createdAt := time.Now().Format(time.RFC3339)
		ownerDid := user.Did

		pullAt, err := db.GetPullAt(s.db, f.RepoAt, pull.PullId)
		if err != nil {
			log.Println("failed to get pull at", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			return
		}

		atUri := f.RepoAt.String()
		client, _ := s.auth.AuthorizedClient(r)
		atResp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoPullCommentNSID,
			Repo:       user.Did,
			Rkey:       appview.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoPullComment{
					Repo:      &atUri,
					Pull:      string(pullAt),
					Owner:     &ownerDid,
					Body:      &body,
					CreatedAt: &createdAt,
				},
			},
		})
		if err != nil {
			log.Println("failed to create pull comment", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			return
		}

		// Create the pull comment in the database with the commentAt field
		commentId, err := db.NewPullComment(tx, &db.PullComment{
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
			return
		}

		// Commit the transaction
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
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		us, err := NewUnsignedClient(f.Knot, s.config.Dev)
		if err != nil {
			log.Printf("failed to create unsigned client for %s", f.Knot)
			s.pages.Error503(w)
			return
		}

		resp, err := us.Branches(f.OwnerDid(), f.RepoName)
		if err != nil {
			log.Println("failed to reach knotserver", err)
			return
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error reading response body: %v", err)
			return
		}

		var result types.RepoBranchesResponse
		err = json.Unmarshal(body, &result)
		if err != nil {
			log.Println("failed to parse response:", err)
			return
		}

		s.pages.RepoNewPull(w, pages.RepoNewPullParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(s, user),
			Branches:     result.Branches,
		})
	case http.MethodPost:
		title := r.FormValue("title")
		body := r.FormValue("body")
		targetBranch := r.FormValue("targetBranch")
		fromFork := r.FormValue("fork")
		sourceBranch := r.FormValue("sourceBranch")
		patch := r.FormValue("patch")

		if targetBranch == "" {
			s.pages.Notice(w, "pull", "Target branch is required.")
			return
		}

		// Determine PR type based on input parameters
		isPushAllowed := f.RepoInfo(s, user).Roles.IsPushAllowed()
		isBranchBased := isPushAllowed && sourceBranch != "" && fromFork == ""
		isForkBased := fromFork != "" && sourceBranch != ""
		isPatchBased := patch != "" && !isBranchBased && !isForkBased

		if isPatchBased && !patchutil.IsFormatPatch(patch) {
			if title == "" {
				s.pages.Notice(w, "pull", "Title is required for git-diff patches.")
				return
			}
		}

		// Validate we have at least one valid PR creation method
		if !isBranchBased && !isPatchBased && !isForkBased {
			s.pages.Notice(w, "pull", "Neither source branch nor patch supplied.")
			return
		}

		// Can't mix branch-based and patch-based approaches
		if isBranchBased && patch != "" {
			s.pages.Notice(w, "pull", "Cannot select both patch and source branch.")
			return
		}

		us, err := NewUnsignedClient(f.Knot, s.config.Dev)
		if err != nil {
			log.Printf("failed to create unsigned client to %s: %v", f.Knot, err)
			s.pages.Notice(w, "pull", "Failed to create a pull request. Try again later.")
			return
		}

		caps, err := us.Capabilities()
		if err != nil {
			log.Println("error fetching knot caps", f.Knot, err)
			s.pages.Notice(w, "pull", "Failed to create a pull request. Try again later.")
			return
		}

		if !caps.PullRequests.FormatPatch {
			s.pages.Notice(w, "pull", "This knot doesn't support format-patch. Unfortunately, there is no fallback for now.")
			return
		}

		// Handle the PR creation based on the type
		if isBranchBased {
			if !caps.PullRequests.BranchSubmissions {
				s.pages.Notice(w, "pull", "This knot doesn't support branch-based pull requests. Try another way?")
				return
			}
			s.handleBranchBasedPull(w, r, f, user, title, body, targetBranch, sourceBranch)
		} else if isForkBased {
			if !caps.PullRequests.ForkSubmissions {
				s.pages.Notice(w, "pull", "This knot doesn't support fork-based pull requests. Try another way?")
				return
			}
			s.handleForkBasedPull(w, r, f, user, fromFork, title, body, targetBranch, sourceBranch)
		} else if isPatchBased {
			if !caps.PullRequests.PatchSubmissions {
				s.pages.Notice(w, "pull", "This knot doesn't support patch-based pull requests. Send your patch over email.")
				return
			}
			s.handlePatchBasedPull(w, r, f, user, title, body, targetBranch, patch)
		}
		return
	}
}

func (s *State) handleBranchBasedPull(w http.ResponseWriter, r *http.Request, f *FullyResolvedRepo, user *auth.User, title, body, targetBranch, sourceBranch string) {
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
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	comparison, err := ksClient.Compare(f.OwnerDid(), f.RepoName, targetBranch, sourceBranch)
	if err != nil {
		log.Println("failed to compare", err)
		s.pages.Notice(w, "pull", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch

	if !patchutil.IsPatchValid(patch) {
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	s.createPullRequest(w, r, f, user, title, body, targetBranch, patch, sourceRev, pullSource, recordPullSource)
}

func (s *State) handlePatchBasedPull(w http.ResponseWriter, r *http.Request, f *FullyResolvedRepo, user *auth.User, title, body, targetBranch, patch string) {
	if !patchutil.IsPatchValid(patch) {
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	s.createPullRequest(w, r, f, user, title, body, targetBranch, patch, "", nil, nil)
}

func (s *State) handleForkBasedPull(w http.ResponseWriter, r *http.Request, f *FullyResolvedRepo, user *auth.User, forkRepo string, title, body, targetBranch, sourceBranch string) {
	fork, err := db.GetForkByDid(s.db, user.Did, forkRepo)
	if errors.Is(err, sql.ErrNoRows) {
		s.pages.Notice(w, "pull", "No such fork.")
		return
	} else if err != nil {
		log.Println("failed to fetch fork:", err)
		s.pages.Notice(w, "pull", "Failed to fetch fork.")
		return
	}

	secret, err := db.GetRegistrationKey(s.db, fork.Knot)
	if err != nil {
		log.Println("failed to fetch registration key:", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	sc, err := NewSignedClient(fork.Knot, secret, s.config.Dev)
	if err != nil {
		log.Println("failed to create signed client:", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	us, err := NewUnsignedClient(fork.Knot, s.config.Dev)
	if err != nil {
		log.Println("failed to create unsigned client:", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	resp, err := sc.NewHiddenRef(user.Did, fork.Name, sourceBranch, targetBranch)
	if err != nil {
		log.Println("failed to create hidden ref:", err, resp.StatusCode)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	switch resp.StatusCode {
	case 404:
	case 400:
		s.pages.Notice(w, "pull", "Branch based pull requests are not supported on this knot.")
		return
	}

	hiddenRef := fmt.Sprintf("hidden/%s/%s", sourceBranch, targetBranch)
	// We're now comparing the sourceBranch (on the fork) against the hiddenRef which is tracking
	// the targetBranch on the target repository. This code is a bit confusing, but here's an example:
	// hiddenRef: hidden/feature-1/main (on repo-fork)
	// targetBranch: main (on repo-1)
	// sourceBranch: feature-1 (on repo-fork)
	comparison, err := us.Compare(user.Did, fork.Name, hiddenRef, sourceBranch)
	if err != nil {
		log.Println("failed to compare across branches", err)
		s.pages.Notice(w, "pull", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch

	if !patchutil.IsPatchValid(patch) {
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	forkAtUri, err := syntax.ParseATURI(fork.AtUri)
	if err != nil {
		log.Println("failed to parse fork AT URI", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	s.createPullRequest(w, r, f, user, title, body, targetBranch, patch, sourceRev, &db.PullSource{
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
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	// We've already checked earlier if it's diff-based and title is empty,
	// so if it's still empty now, it's intentionally skipped owing to format-patch.
	if title == "" {
		formatPatches, err := patchutil.ExtractPatches(patch)
		if err != nil {
			s.pages.Notice(w, "pull", fmt.Sprintf("Failed to extract patches: %v", err))
			return
		}
		if len(formatPatches) == 0 {
			s.pages.Notice(w, "pull", "No patches found in the supplied format-patch.")
			return
		}

		title = formatPatches[0].Title
		body = formatPatches[0].Body
	}

	rkey := appview.TID()
	initialSubmission := db.PullSubmission{
		Patch:     patch,
		SourceRev: sourceRev,
	}
	err = db.NewPull(tx, &db.Pull{
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
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}
	client, _ := s.auth.AuthorizedClient(r)
	pullId, err := db.NextPullId(s.db, f.RepoAt)
	if err != nil {
		log.Println("failed to get pull id", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
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
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pullId))
}

func (s *State) ValidatePatch(w http.ResponseWriter, r *http.Request) {
	_, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	patch := r.FormValue("patch")
	if patch == "" {
		s.pages.Notice(w, "patch-error", "Patch is required.")
		return
	}

	if patch == "" || !patchutil.IsPatchValid(patch) {
		s.pages.Notice(w, "patch-error", "Invalid patch format. Please provide a valid git diff or format-patch.")
		return
	}

	if patchutil.IsFormatPatch(patch) {
		s.pages.Notice(w, "patch-preview", "git-format-patch detected. Title and description are optional; if left out, they will be extracted from the first commit.")
	} else {
		s.pages.Notice(w, "patch-preview", "Regular git-diff detected. Please provide a title and description.")
	}
}

func (s *State) PatchUploadFragment(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	s.pages.PullPatchUploadFragment(w, pages.PullPatchUploadParams{
		RepoInfo: f.RepoInfo(s, user),
	})
}

func (s *State) CompareBranchesFragment(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	us, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", f.Knot)
		s.pages.Error503(w)
		return
	}

	resp, err := us.Branches(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	var result types.RepoBranchesResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		return
	}

	s.pages.PullCompareBranchesFragment(w, pages.PullCompareBranchesParams{
		RepoInfo: f.RepoInfo(s, user),
		Branches: result.Branches,
	})
}

func (s *State) CompareForksFragment(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	forks, err := db.GetForksByDid(s.db, user.Did)
	if err != nil {
		log.Println("failed to get forks", err)
		return
	}

	s.pages.PullCompareForkFragment(w, pages.PullCompareForkParams{
		RepoInfo: f.RepoInfo(s, user),
		Forks:    forks,
	})
}

func (s *State) CompareForksBranchesFragment(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	forkVal := r.URL.Query().Get("fork")

	// fork repo
	repo, err := db.GetRepo(s.db, user.Did, forkVal)
	if err != nil {
		log.Println("failed to get repo", user.Did, forkVal)
		return
	}

	sourceBranchesClient, err := NewUnsignedClient(repo.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", repo.Knot)
		s.pages.Error503(w)
		return
	}

	sourceResp, err := sourceBranchesClient.Branches(user.Did, repo.Name)
	if err != nil {
		log.Println("failed to reach knotserver for source branches", err)
		return
	}

	sourceBody, err := io.ReadAll(sourceResp.Body)
	if err != nil {
		log.Println("failed to read source response body", err)
		return
	}
	defer sourceResp.Body.Close()

	var sourceResult types.RepoBranchesResponse
	err = json.Unmarshal(sourceBody, &sourceResult)
	if err != nil {
		log.Println("failed to parse source branches response:", err)
		return
	}

	targetBranchesClient, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for target knot %s", f.Knot)
		s.pages.Error503(w)
		return
	}

	targetResp, err := targetBranchesClient.Branches(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver for target branches", err)
		return
	}

	targetBody, err := io.ReadAll(targetResp.Body)
	if err != nil {
		log.Println("failed to read target response body", err)
		return
	}
	defer targetResp.Body.Close()

	var targetResult types.RepoBranchesResponse
	err = json.Unmarshal(targetBody, &targetResult)
	if err != nil {
		log.Println("failed to parse target branches response:", err)
		return
	}

	s.pages.PullCompareForkBranchesFragment(w, pages.PullCompareForkBranchesParams{
		RepoInfo:       f.RepoInfo(s, user),
		SourceBranches: sourceResult.Branches,
		TargetBranches: targetResult.Branches,
	})
}

func (s *State) ResubmitPull(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.pages.PullResubmitFragment(w, pages.PullResubmitParams{
			RepoInfo: f.RepoInfo(s, user),
			Pull:     pull,
		})
		return
	case http.MethodPost:
		if pull.IsPatchBased() {
			s.resubmitPatch(w, r)
			return
		} else if pull.IsBranchBased() {
			s.resubmitBranch(w, r)
			return
		} else if pull.IsForkBased() {
			s.resubmitFork(w, r)
			return
		}
	}
}

func (s *State) resubmitPatch(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	if user.Did != pull.OwnerDid {
		log.Println("unauthorized user")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	patch := r.FormValue("patch")

	if err = validateResubmittedPatch(pull, patch); err != nil {
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	err = db.ResubmitPull(tx, pull, patch, "")
	if err != nil {
		log.Println("failed to resubmit pull request", err)
		s.pages.Notice(w, "resubmit-error", "Failed to resubmit pull request. Try again later.")
		return
	}
	client, _ := s.auth.AuthorizedClient(r)

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoPullNSID, user.Did, pull.Rkey)
	if err != nil {
		// failed to get record
		s.pages.Notice(w, "resubmit-error", "Failed to update pull, no record found on PDS.")
		return
	}

	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
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
		s.pages.Notice(w, "resubmit-error", "Failed to update pull request on the PDS. Try again later.")
		return
	}

	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		s.pages.Notice(w, "resubmit-error", "Failed to resubmit pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}

func (s *State) resubmitBranch(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "resubmit-error", "Failed to edit patch. Try again later.")
		return
	}

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	if user.Did != pull.OwnerDid {
		log.Println("unauthorized user")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if !f.RepoInfo(s, user).Roles.IsPushAllowed() {
		log.Println("unauthorized user")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	ksClient, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create client for %s: %s", f.Knot, err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	comparison, err := ksClient.Compare(f.OwnerDid(), f.RepoName, pull.TargetBranch, pull.PullSource.Branch)
	if err != nil {
		log.Printf("compare request failed: %s", err)
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch

	if err = validateResubmittedPatch(pull, patch); err != nil {
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	if sourceRev == pull.Submissions[pull.LastRoundNumber()].SourceRev {
		s.pages.Notice(w, "resubmit-error", "This branch has not changed since the last submission.")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	err = db.ResubmitPull(tx, pull, patch, sourceRev)
	if err != nil {
		log.Println("failed to create pull request", err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	client, _ := s.auth.AuthorizedClient(r)

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoPullNSID, user.Did, pull.Rkey)
	if err != nil {
		// failed to get record
		s.pages.Notice(w, "resubmit-error", "Failed to update pull, no record found on PDS.")
		return
	}

	recordPullSource := &tangled.RepoPull_Source{
		Branch: pull.PullSource.Branch,
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
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
		s.pages.Notice(w, "resubmit-error", "Failed to update pull request on the PDS. Try again later.")
		return
	}

	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		s.pages.Notice(w, "resubmit-error", "Failed to resubmit pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}

func (s *State) resubmitFork(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "resubmit-error", "Failed to edit patch. Try again later.")
		return
	}

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	if user.Did != pull.OwnerDid {
		log.Println("unauthorized user")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	forkRepo, err := db.GetRepoByAtUri(s.db, pull.PullSource.RepoAt.String())
	if err != nil {
		log.Println("failed to get source repo", err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	// extract patch by performing compare
	ksClient, err := NewUnsignedClient(forkRepo.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create client for %s: %s", forkRepo.Knot, err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	secret, err := db.GetRegistrationKey(s.db, forkRepo.Knot)
	if err != nil {
		log.Printf("failed to get registration key for %s: %s", forkRepo.Knot, err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	// update the hidden tracking branch to latest
	signedClient, err := NewSignedClient(forkRepo.Knot, secret, s.config.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s: %s", forkRepo.Knot, err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	resp, err := signedClient.NewHiddenRef(forkRepo.Did, forkRepo.Name, pull.PullSource.Branch, pull.TargetBranch)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		log.Printf("failed to update tracking branch: %s", err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	hiddenRef := url.QueryEscape(fmt.Sprintf("hidden/%s/%s", pull.PullSource.Branch, pull.TargetBranch))
	comparison, err := ksClient.Compare(forkRepo.Did, forkRepo.Name, hiddenRef, pull.PullSource.Branch)
	if err != nil {
		log.Printf("failed to compare branches: %s", err)
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch

	if err = validateResubmittedPatch(pull, patch); err != nil {
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	if sourceRev == pull.Submissions[pull.LastRoundNumber()].SourceRev {
		s.pages.Notice(w, "resubmit-error", "This branch has not changed since the last submission.")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	err = db.ResubmitPull(tx, pull, patch, sourceRev)
	if err != nil {
		log.Println("failed to create pull request", err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}
	client, _ := s.auth.AuthorizedClient(r)

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoPullNSID, user.Did, pull.Rkey)
	if err != nil {
		// failed to get record
		s.pages.Notice(w, "resubmit-error", "Failed to update pull, no record found on PDS.")
		return
	}

	repoAt := pull.PullSource.RepoAt.String()
	recordPullSource := &tangled.RepoPull_Source{
		Branch: pull.PullSource.Branch,
		Repo:   &repoAt,
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
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
		s.pages.Notice(w, "resubmit-error", "Failed to update pull request on the PDS. Try again later.")
		return
	}

	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
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
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to resolve repo:", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	secret, err := db.GetRegistrationKey(s.db, f.Knot)
	if err != nil {
		log.Printf("no registration key found for domain %s: %s\n", f.Knot, err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	ident, err := s.resolver.ResolveIdent(r.Context(), pull.OwnerDid)
	if err != nil {
		log.Printf("resolving identity: %s", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	email, err := db.GetPrimaryEmail(s.db, pull.OwnerDid)
	if err != nil {
		log.Printf("failed to get primary email: %s", err)
	}

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s: %s", f.Knot, err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	// Merge the pull request
	resp, err := ksClient.Merge([]byte(pull.LatestPatch()), f.OwnerDid(), f.RepoName, pull.TargetBranch, pull.Title, pull.Body, ident.Handle.String(), email.Address)
	if err != nil {
		log.Printf("failed to merge pull request: %s", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	if resp.StatusCode == http.StatusOK {
		err := db.MergePull(s.db, f.RepoAt, pull.PullId)
		if err != nil {
			log.Printf("failed to update pull request status in database: %s", err)
			s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
			return
		}
		s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s/pulls/%d", f.OwnerHandle(), f.RepoName, pull.PullId))
	} else {
		log.Printf("knotserver returned non-OK status code for merge: %d", resp.StatusCode)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
	}
}

func (s *State) ClosePull(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("malformed middleware")
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// auth filter: only owner or collaborators can close
	roles := RolesInRepo(s, user, f)
	isCollaborator := roles.IsCollaborator()
	isPullAuthor := user.Did == pull.OwnerDid
	isCloseAllowed := isCollaborator || isPullAuthor
	if !isCloseAllowed {
		log.Println("failed to close pull")
		s.pages.Notice(w, "pull-close", "You are unauthorized to close this pull.")
		return
	}

	// Start a transaction
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start transaction", err)
		s.pages.Notice(w, "pull-close", "Failed to close pull.")
		return
	}

	// Close the pull in the database
	err = db.ClosePull(tx, f.RepoAt, pull.PullId)
	if err != nil {
		log.Println("failed to close pull", err)
		s.pages.Notice(w, "pull-close", "Failed to close pull.")
		return
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		s.pages.Notice(w, "pull-close", "Failed to close pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}

func (s *State) ReopenPull(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to resolve repo", err)
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// auth filter: only owner or collaborators can close
	roles := RolesInRepo(s, user, f)
	isCollaborator := roles.IsCollaborator()
	isPullAuthor := user.Did == pull.OwnerDid
	isCloseAllowed := isCollaborator || isPullAuthor
	if !isCloseAllowed {
		log.Println("failed to close pull")
		s.pages.Notice(w, "pull-close", "You are unauthorized to close this pull.")
		return
	}

	// Start a transaction
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start transaction", err)
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	// Reopen the pull in the database
	err = db.ReopenPull(tx, f.RepoAt, pull.PullId)
	if err != nil {
		log.Println("failed to reopen pull", err)
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}
