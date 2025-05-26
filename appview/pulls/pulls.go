package pulls

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/resolver"
	"tangled.sh/tangled.sh/core/types"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
)

type Pulls struct {
	oauth        *oauth.OAuth
	repoResolver *reporesolver.RepoResolver
	pages        *pages.Pages
	resolver     *resolver.Resolver
	db           *db.DB
	config       *appview.Config
	posthog      posthog.Client
}

func New(oauth *oauth.OAuth, repoResolver *reporesolver.RepoResolver, pages *pages.Pages, resolver *resolver.Resolver, db *db.DB, config *appview.Config) *Pulls {
	return &Pulls{oauth: oauth, repoResolver: repoResolver, pages: pages, resolver: resolver, db: db, config: config}
}

// htmx fragment
func (s *Pulls) PullActions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user := s.oauth.GetUser(r)
		f, err := s.repoResolver.Resolve(r)
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

		// can be nil  if this pull is not stacked
		stack, _ := r.Context().Value("stack").(db.Stack)

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

		mergeCheckResponse := s.mergeCheck(f, pull, stack)
		resubmitResult := pages.Unknown
		if user.Did == pull.OwnerDid {
			resubmitResult = s.resubmitCheck(f, pull, stack)
		}

		s.pages.PullActionsFragment(w, pages.PullActionsParams{
			LoggedInUser:  user,
			RepoInfo:      f.RepoInfo(user),
			Pull:          pull,
			RoundNumber:   roundNumber,
			MergeCheck:    mergeCheckResponse,
			ResubmitCheck: resubmitResult,
			Stack:         stack,
		})
		return
	}
}

func (s *Pulls) RepoSinglePull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
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

	// can be nil  if this pull is not stacked
	stack, _ := r.Context().Value("stack").(db.Stack)
	abandonedPulls, _ := r.Context().Value("abandonedPulls").([]*db.Pull)

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

	mergeCheckResponse := s.mergeCheck(f, pull, stack)
	resubmitResult := pages.Unknown
	if user != nil && user.Did == pull.OwnerDid {
		resubmitResult = s.resubmitCheck(f, pull, stack)
	}

	s.pages.RepoSinglePull(w, pages.RepoSinglePullParams{
		LoggedInUser:   user,
		RepoInfo:       f.RepoInfo(user),
		DidHandleMap:   didHandleMap,
		Pull:           pull,
		Stack:          stack,
		AbandonedPulls: abandonedPulls,
		MergeCheck:     mergeCheckResponse,
		ResubmitCheck:  resubmitResult,
	})
}

func (s *Pulls) mergeCheck(f *reporesolver.ResolvedRepo, pull *db.Pull, stack db.Stack) types.MergeCheckResponse {
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

	ksClient, err := knotclient.NewSignedClient(f.Knot, secret, s.config.Core.Dev)
	if err != nil {
		log.Printf("failed to setup signed client for %s; ignoring: %v", f.Knot, err)
		return types.MergeCheckResponse{
			Error: "failed to check merge status",
		}
	}

	patch := pull.LatestPatch()
	if pull.IsStacked() {
		// combine patches of substack
		subStack := stack.Below(pull)
		// collect the portion of the stack that is mergeable
		mergeable := subStack.Mergeable()
		// combine each patch
		patch = mergeable.CombinedPatch()
	}

	resp, err := ksClient.MergeCheck([]byte(patch), f.OwnerDid(), f.RepoName, pull.TargetBranch)
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

func (s *Pulls) resubmitCheck(f *reporesolver.ResolvedRepo, pull *db.Pull, stack db.Stack) pages.ResubmitResult {
	if pull.State == db.PullMerged || pull.State == db.PullDeleted || pull.PullSource == nil {
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

	us, err := knotclient.NewUnsignedClient(knot, s.config.Core.Dev)
	if err != nil {
		log.Printf("failed to setup client for %s; ignoring: %v", knot, err)
		return pages.Unknown
	}

	result, err := us.Branch(ownerDid, repoName, pull.PullSource.Branch)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return pages.Unknown
	}

	latestSourceRev := pull.Submissions[pull.LastRoundNumber()].SourceRev

	if pull.IsStacked() && stack != nil {
		top := stack[0]
		latestSourceRev = top.Submissions[top.LastRoundNumber()].SourceRev
	}

	log.Println(latestSourceRev, result.Branch.Hash)

	if latestSourceRev != result.Branch.Hash {
		return pages.ShouldResubmit
	}

	return pages.ShouldNotResubmit
}

func (s *Pulls) RepoPullPatch(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
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

	stack, _ := r.Context().Value("stack").(db.Stack)

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

	patch := pull.Submissions[roundIdInt].Patch
	diff := patchutil.AsNiceDiff(patch, pull.TargetBranch)

	s.pages.RepoPullPatchPage(w, pages.RepoPullPatchParams{
		LoggedInUser: user,
		DidHandleMap: didHandleMap,
		RepoInfo:     f.RepoInfo(user),
		Pull:         pull,
		Stack:        stack,
		Round:        roundIdInt,
		Submission:   pull.Submissions[roundIdInt],
		Diff:         &diff,
	})

}

func (s *Pulls) RepoPullInterdiff(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	f, err := s.repoResolver.Resolve(r)
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

	currentPatch, err := patchutil.AsDiff(pull.Submissions[roundIdInt].Patch)
	if err != nil {
		log.Println("failed to interdiff; current patch malformed")
		s.pages.Notice(w, fmt.Sprintf("interdiff-error-%d", roundIdInt), "Failed to calculate interdiff; current patch is invalid.")
		return
	}

	previousPatch, err := patchutil.AsDiff(pull.Submissions[roundIdInt-1].Patch)
	if err != nil {
		log.Println("failed to interdiff; previous patch malformed")
		s.pages.Notice(w, fmt.Sprintf("interdiff-error-%d", roundIdInt), "Failed to calculate interdiff; previous patch is invalid.")
		return
	}

	interdiff := patchutil.Interdiff(previousPatch, currentPatch)

	s.pages.RepoPullInterdiffPage(w, pages.RepoPullInterdiffParams{
		LoggedInUser: s.oauth.GetUser(r),
		RepoInfo:     f.RepoInfo(user),
		Pull:         pull,
		Round:        roundIdInt,
		DidHandleMap: didHandleMap,
		Interdiff:    interdiff,
	})
	return
}

func (s *Pulls) RepoPullPatchRaw(w http.ResponseWriter, r *http.Request) {
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

func (s *Pulls) RepoPulls(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	params := r.URL.Query()

	state := db.PullOpen
	switch params.Get("state") {
	case "closed":
		state = db.PullClosed
	case "merged":
		state = db.PullMerged
	}

	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pulls, err := db.GetPulls(
		s.db,
		db.FilterEq("repo_at", f.RepoAt),
		db.FilterEq("state", state),
	)
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
		LoggedInUser: s.oauth.GetUser(r),
		RepoInfo:     f.RepoInfo(user),
		Pulls:        pulls,
		DidHandleMap: didHandleMap,
		FilteringBy:  state,
	})
	return
}

func (s *Pulls) PullComment(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
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
			RepoInfo:     f.RepoInfo(user),
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
		client, err := s.oauth.AuthorizedClient(r)
		if err != nil {
			log.Println("failed to get authorized client", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			return
		}
		atResp, err := client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
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

		if !s.config.Core.Dev {
			err = s.posthog.Enqueue(posthog.Capture{
				DistinctId: user.Did,
				Event:      "new_pull_comment",
				Properties: posthog.Properties{"repo_at": f.RepoAt.String(), "pull_id": pull.PullId},
			})
			if err != nil {
				log.Println("failed to enqueue posthog event:", err)
			}
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d#comment-%d", f.OwnerSlashRepo(), pull.PullId, commentId))
		return
	}
}

func (s *Pulls) NewPull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		us, err := knotclient.NewUnsignedClient(f.Knot, s.config.Core.Dev)
		if err != nil {
			log.Printf("failed to create unsigned client for %s", f.Knot)
			s.pages.Error503(w)
			return
		}

		result, err := us.Branches(f.OwnerDid(), f.RepoName)
		if err != nil {
			log.Println("failed to fetch branches", err)
			return
		}

		// can be one of "patch", "branch" or "fork"
		strategy := r.URL.Query().Get("strategy")
		// ignored if strategy is "patch"
		sourceBranch := r.URL.Query().Get("sourceBranch")
		targetBranch := r.URL.Query().Get("targetBranch")

		s.pages.RepoNewPull(w, pages.RepoNewPullParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(user),
			Branches:     result.Branches,
			Strategy:     strategy,
			SourceBranch: sourceBranch,
			TargetBranch: targetBranch,
			Title:        r.URL.Query().Get("title"),
			Body:         r.URL.Query().Get("body"),
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
		isPushAllowed := f.RepoInfo(user).Roles.IsPushAllowed()
		isBranchBased := isPushAllowed && sourceBranch != "" && fromFork == ""
		isForkBased := fromFork != "" && sourceBranch != ""
		isPatchBased := patch != "" && !isBranchBased && !isForkBased
		isStacked := r.FormValue("isStacked") == "on"

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

		us, err := knotclient.NewUnsignedClient(f.Knot, s.config.Core.Dev)
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
			s.handleBranchBasedPull(w, r, f, user, title, body, targetBranch, sourceBranch, isStacked)
		} else if isForkBased {
			if !caps.PullRequests.ForkSubmissions {
				s.pages.Notice(w, "pull", "This knot doesn't support fork-based pull requests. Try another way?")
				return
			}
			s.handleForkBasedPull(w, r, f, user, fromFork, title, body, targetBranch, sourceBranch, isStacked)
		} else if isPatchBased {
			if !caps.PullRequests.PatchSubmissions {
				s.pages.Notice(w, "pull", "This knot doesn't support patch-based pull requests. Send your patch over email.")
				return
			}
			s.handlePatchBasedPull(w, r, f, user, title, body, targetBranch, patch, isStacked)
		}
		return
	}
}

func (s *Pulls) handleBranchBasedPull(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	title,
	body,
	targetBranch,
	sourceBranch string,
	isStacked bool,
) {
	pullSource := &db.PullSource{
		Branch: sourceBranch,
	}
	recordPullSource := &tangled.RepoPull_Source{
		Branch: sourceBranch,
	}

	// Generate a patch using /compare
	ksClient, err := knotclient.NewUnsignedClient(f.Knot, s.config.Core.Dev)
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

	s.createPullRequest(w, r, f, user, title, body, targetBranch, patch, sourceRev, pullSource, recordPullSource, isStacked)
}

func (s *Pulls) handlePatchBasedPull(w http.ResponseWriter, r *http.Request, f *reporesolver.ResolvedRepo, user *oauth.User, title, body, targetBranch, patch string, isStacked bool) {
	if !patchutil.IsPatchValid(patch) {
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	s.createPullRequest(w, r, f, user, title, body, targetBranch, patch, "", nil, nil, isStacked)
}

func (s *Pulls) handleForkBasedPull(w http.ResponseWriter, r *http.Request, f *reporesolver.ResolvedRepo, user *oauth.User, forkRepo string, title, body, targetBranch, sourceBranch string, isStacked bool) {
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

	sc, err := knotclient.NewSignedClient(fork.Knot, secret, s.config.Core.Dev)
	if err != nil {
		log.Println("failed to create signed client:", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	us, err := knotclient.NewUnsignedClient(fork.Knot, s.config.Core.Dev)
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
	}, &tangled.RepoPull_Source{Branch: sourceBranch, Repo: &fork.AtUri}, isStacked)
}

func (s *Pulls) createPullRequest(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	title, body, targetBranch string,
	patch string,
	sourceRev string,
	pullSource *db.PullSource,
	recordPullSource *tangled.RepoPull_Source,
	isStacked bool,
) {
	if isStacked {
		// creates a series of PRs, each linking to the previous, identified by jj's change-id
		s.createStackedPulLRequest(
			w,
			r,
			f,
			user,
			targetBranch,
			patch,
			sourceRev,
			pullSource,
		)
		return
	}

	client, err := s.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to get authorized client", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

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
	pullId, err := db.NextPullId(tx, f.RepoAt)
	if err != nil {
		log.Println("failed to get pull id", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
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

	if err = tx.Commit(); err != nil {
		log.Println("failed to create pull request", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	if !s.config.Core.Dev {
		err = s.posthog.Enqueue(posthog.Capture{
			DistinctId: user.Did,
			Event:      "new_pull",
			Properties: posthog.Properties{"repo_at": f.RepoAt.String(), "pull_id": pullId},
		})
		if err != nil {
			log.Println("failed to enqueue posthog event:", err)
		}
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pullId))
}

func (s *Pulls) createStackedPulLRequest(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	targetBranch string,
	patch string,
	sourceRev string,
	pullSource *db.PullSource,
) {
	// run some necessary checks for stacked-prs first

	//  must be branch or fork based
	if sourceRev == "" {
		log.Println("stacked PR from patch-based pull")
		s.pages.Notice(w, "pull", "Stacking is only supported on branch and fork based pull-requests.")
		return
	}

	formatPatches, err := patchutil.ExtractPatches(patch)
	if err != nil {
		log.Println("failed to extract patches", err)
		s.pages.Notice(w, "pull", fmt.Sprintf("Failed to extract patches: %v", err))
		return
	}

	//  must have atleast 1 patch to begin with
	if len(formatPatches) == 0 {
		log.Println("empty patches")
		s.pages.Notice(w, "pull", "No patches found in the generated format-patch.")
		return
	}

	// build a stack out of this patch
	stackId := uuid.New()
	stack, err := newStack(f, user, targetBranch, patch, pullSource, stackId.String())
	if err != nil {
		log.Println("failed to create stack", err)
		s.pages.Notice(w, "pull", fmt.Sprintf("Failed to create stack: %v", err))
		return
	}

	client, err := s.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to get authorized client", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	// apply all record creations at once
	var writes []*comatproto.RepoApplyWrites_Input_Writes_Elem
	for _, p := range stack {
		record := p.AsRecord()
		write := comatproto.RepoApplyWrites_Input_Writes_Elem{
			RepoApplyWrites_Create: &comatproto.RepoApplyWrites_Create{
				Collection: tangled.RepoPullNSID,
				Rkey:       &p.Rkey,
				Value: &lexutil.LexiconTypeDecoder{
					Val: &record,
				},
			},
		}
		writes = append(writes, &write)
	}
	_, err = client.RepoApplyWrites(r.Context(), &comatproto.RepoApplyWrites_Input{
		Repo:   user.Did,
		Writes: writes,
	})
	if err != nil {
		log.Println("failed to create stacked pull request", err)
		s.pages.Notice(w, "pull", "Failed to create stacked pull request. Try again later.")
		return
	}

	// create all pulls at once
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	for _, p := range stack {
		err = db.NewPull(tx, p)
		if err != nil {
			log.Println("failed to create pull request", err)
			s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		log.Println("failed to create pull request", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls", f.OwnerSlashRepo()))
}

func (s *Pulls) ValidatePatch(w http.ResponseWriter, r *http.Request) {
	_, err := s.repoResolver.Resolve(r)
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

func (s *Pulls) PatchUploadFragment(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	s.pages.PullPatchUploadFragment(w, pages.PullPatchUploadParams{
		RepoInfo: f.RepoInfo(user),
	})
}

func (s *Pulls) CompareBranchesFragment(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	us, err := knotclient.NewUnsignedClient(f.Knot, s.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", f.Knot)
		s.pages.Error503(w)
		return
	}

	result, err := us.Branches(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	branches := result.Branches
	sort.Slice(branches, func(i int, j int) bool {
		return branches[i].Commit.Committer.When.After(branches[j].Commit.Committer.When)
	})

	withoutDefault := []types.Branch{}
	for _, b := range branches {
		if b.IsDefault {
			continue
		}
		withoutDefault = append(withoutDefault, b)
	}

	s.pages.PullCompareBranchesFragment(w, pages.PullCompareBranchesParams{
		RepoInfo: f.RepoInfo(user),
		Branches: withoutDefault,
	})
}

func (s *Pulls) CompareForksFragment(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
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
		RepoInfo: f.RepoInfo(user),
		Forks:    forks,
		Selected: r.URL.Query().Get("fork"),
	})
}

func (s *Pulls) CompareForksBranchesFragment(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	f, err := s.repoResolver.Resolve(r)
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

	sourceBranchesClient, err := knotclient.NewUnsignedClient(repo.Knot, s.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", repo.Knot)
		s.pages.Error503(w)
		return
	}

	sourceResult, err := sourceBranchesClient.Branches(user.Did, repo.Name)
	if err != nil {
		log.Println("failed to reach knotserver for source branches", err)
		return
	}

	targetBranchesClient, err := knotclient.NewUnsignedClient(f.Knot, s.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for target knot %s", f.Knot)
		s.pages.Error503(w)
		return
	}

	targetResult, err := targetBranchesClient.Branches(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver for target branches", err)
		return
	}

	sourceBranches := sourceResult.Branches
	sort.Slice(sourceBranches, func(i int, j int) bool {
		return sourceBranches[i].Commit.Committer.When.After(sourceBranches[j].Commit.Committer.When)
	})

	s.pages.PullCompareForkBranchesFragment(w, pages.PullCompareForkBranchesParams{
		RepoInfo:       f.RepoInfo(user),
		SourceBranches: sourceBranches,
		TargetBranches: targetResult.Branches,
	})
}

func (s *Pulls) ResubmitPull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
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
			RepoInfo: f.RepoInfo(user),
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

func (s *Pulls) resubmitPatch(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	f, err := s.repoResolver.Resolve(r)
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

	s.resubmitPullHelper(w, r, f, user, pull, patch, "")
}

func (s *Pulls) resubmitBranch(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "resubmit-error", "Failed to edit patch. Try again later.")
		return
	}

	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	if user.Did != pull.OwnerDid {
		log.Println("unauthorized user")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if !f.RepoInfo(user).Roles.IsPushAllowed() {
		log.Println("unauthorized user")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	ksClient, err := knotclient.NewUnsignedClient(f.Knot, s.config.Core.Dev)
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

	s.resubmitPullHelper(w, r, f, user, pull, patch, sourceRev)
}

func (s *Pulls) resubmitFork(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "resubmit-error", "Failed to edit patch. Try again later.")
		return
	}

	f, err := s.repoResolver.Resolve(r)
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
	ksClient, err := knotclient.NewUnsignedClient(forkRepo.Knot, s.config.Core.Dev)
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
	signedClient, err := knotclient.NewSignedClient(forkRepo.Knot, secret, s.config.Core.Dev)
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

	hiddenRef := fmt.Sprintf("hidden/%s/%s", pull.PullSource.Branch, pull.TargetBranch)
	comparison, err := ksClient.Compare(forkRepo.Did, forkRepo.Name, hiddenRef, pull.PullSource.Branch)
	if err != nil {
		log.Printf("failed to compare branches: %s", err)
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.Patch

	s.resubmitPullHelper(w, r, f, user, pull, patch, sourceRev)
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

func (s *Pulls) resubmitPullHelper(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	pull *db.Pull,
	patch string,
	sourceRev string,
) {
	if pull.IsStacked() {
		log.Println("resubmitting stacked PR")
		s.resubmitStackedPullHelper(w, r, f, user, pull, patch, pull.StackId)
		return
	}

	if err := validateResubmittedPatch(pull, patch); err != nil {
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	// validate sourceRev if branch/fork based
	if pull.IsBranchBased() || pull.IsForkBased() {
		if sourceRev == pull.Submissions[pull.LastRoundNumber()].SourceRev {
			s.pages.Notice(w, "resubmit-error", "This branch has not changed since the last submission.")
			return
		}
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
	client, err := s.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to authorize client")
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	ex, err := client.RepoGetRecord(r.Context(), "", tangled.RepoPullNSID, user.Did, pull.Rkey)
	if err != nil {
		// failed to get record
		s.pages.Notice(w, "resubmit-error", "Failed to update pull, no record found on PDS.")
		return
	}

	var recordPullSource *tangled.RepoPull_Source
	if pull.IsBranchBased() {
		recordPullSource = &tangled.RepoPull_Source{
			Branch: pull.PullSource.Branch,
		}
	}
	if pull.IsForkBased() {
		repoAt := pull.PullSource.RepoAt.String()
		recordPullSource = &tangled.RepoPull_Source{
			Branch: pull.PullSource.Branch,
			Repo:   &repoAt,
		}
	}

	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
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

func (s *Pulls) resubmitStackedPullHelper(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	pull *db.Pull,
	patch string,
	stackId string,
) {
	targetBranch := pull.TargetBranch

	origStack, _ := r.Context().Value("stack").(db.Stack)
	newStack, err := newStack(f, user, targetBranch, patch, pull.PullSource, stackId)
	if err != nil {
		log.Println("failed to create resubmitted stack", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	// find the diff between the stacks, first, map them by changeId
	origById := make(map[string]*db.Pull)
	newById := make(map[string]*db.Pull)
	for _, p := range origStack {
		origById[p.ChangeId] = p
	}
	for _, p := range newStack {
		newById[p.ChangeId] = p
	}

	// commits that got deleted: corresponding pull is closed
	// commits that got added: new pull is created
	// commits that got updated: corresponding pull is resubmitted & new round begins
	//
	// for commits that were unchanged: no changes, parent-change-id is updated as necessary
	additions := make(map[string]*db.Pull)
	deletions := make(map[string]*db.Pull)
	unchanged := make(map[string]struct{})
	updated := make(map[string]struct{})

	// pulls in orignal stack but not in new one
	for _, op := range origStack {
		if _, ok := newById[op.ChangeId]; !ok {
			deletions[op.ChangeId] = op
		}
	}

	// pulls in new stack but not in original one
	for _, np := range newStack {
		if _, ok := origById[np.ChangeId]; !ok {
			additions[np.ChangeId] = np
		}
	}

	// NOTE: this loop can be written in any of above blocks,
	// but is written separately in the interest of simpler code
	for _, np := range newStack {
		if op, ok := origById[np.ChangeId]; ok {
			// pull exists in both stacks
			// TODO: can we avoid reparse?
			origFiles, origHeaderStr, _ := gitdiff.Parse(strings.NewReader(op.LatestPatch()))
			newFiles, newHeaderStr, _ := gitdiff.Parse(strings.NewReader(np.LatestPatch()))

			origHeader, _ := gitdiff.ParsePatchHeader(origHeaderStr)
			newHeader, _ := gitdiff.ParsePatchHeader(newHeaderStr)

			patchutil.SortPatch(newFiles)
			patchutil.SortPatch(origFiles)

			// text content of patch may be identical, but a jj rebase might have forwarded it
			//
			// we still need to update the hash in submission.Patch and submission.SourceRev
			if patchutil.Equal(newFiles, origFiles) &&
				origHeader.Title == newHeader.Title &&
				origHeader.Body == newHeader.Body {
				unchanged[op.ChangeId] = struct{}{}
			} else {
				updated[op.ChangeId] = struct{}{}
			}
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		log.Println("failed to start transaction", err)
		s.pages.Notice(w, "pull-resubmit-error", "Failed to resubmit pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	// pds updates to make
	var writes []*comatproto.RepoApplyWrites_Input_Writes_Elem

	// deleted pulls are marked as deleted in the DB
	for _, p := range deletions {
		err := db.DeletePull(tx, p.RepoAt, p.PullId)
		if err != nil {
			log.Println("failed to delete pull", err, p.PullId)
			s.pages.Notice(w, "pull-resubmit-error", "Failed to resubmit pull request. Try again later.")
			return
		}
		writes = append(writes, &comatproto.RepoApplyWrites_Input_Writes_Elem{
			RepoApplyWrites_Delete: &comatproto.RepoApplyWrites_Delete{
				Collection: tangled.RepoPullNSID,
				Rkey:       p.Rkey,
			},
		})
	}

	// new pulls are created
	for _, p := range additions {
		err := db.NewPull(tx, p)
		if err != nil {
			log.Println("failed to create pull", err, p.PullId)
			s.pages.Notice(w, "pull-resubmit-error", "Failed to resubmit pull request. Try again later.")
			return
		}

		record := p.AsRecord()
		writes = append(writes, &comatproto.RepoApplyWrites_Input_Writes_Elem{
			RepoApplyWrites_Create: &comatproto.RepoApplyWrites_Create{
				Collection: tangled.RepoPullNSID,
				Rkey:       &p.Rkey,
				Value: &lexutil.LexiconTypeDecoder{
					Val: &record,
				},
			},
		})
	}

	// updated pulls are, well, updated; to start a new round
	for id := range updated {
		op, _ := origById[id]
		np, _ := newById[id]

		submission := np.Submissions[np.LastRoundNumber()]

		// resubmit the old pull
		err := db.ResubmitPull(tx, op, submission.Patch, submission.SourceRev)

		if err != nil {
			log.Println("failed to update pull", err, op.PullId)
			s.pages.Notice(w, "pull-resubmit-error", "Failed to resubmit pull request. Try again later.")
			return
		}

		record := op.AsRecord()
		record.Patch = submission.Patch

		writes = append(writes, &comatproto.RepoApplyWrites_Input_Writes_Elem{
			RepoApplyWrites_Update: &comatproto.RepoApplyWrites_Update{
				Collection: tangled.RepoPullNSID,
				Rkey:       op.Rkey,
				Value: &lexutil.LexiconTypeDecoder{
					Val: &record,
				},
			},
		})
	}

	// unchanged pulls are edited without starting a new round
	//
	// update source-revs & patches without advancing rounds
	for changeId := range unchanged {
		op, _ := origById[changeId]
		np, _ := newById[changeId]

		origSubmission := op.Submissions[op.LastRoundNumber()]
		newSubmission := np.Submissions[np.LastRoundNumber()]

		log.Println("moving unchanged change id : ", changeId)

		err := db.UpdatePull(
			tx,
			newSubmission.Patch,
			newSubmission.SourceRev,
			db.FilterEq("id", origSubmission.ID),
		)

		if err != nil {
			log.Println("failed to update pull", err, op.PullId)
			s.pages.Notice(w, "pull-resubmit-error", "Failed to resubmit pull request. Try again later.")
			return
		}

		record := op.AsRecord()
		record.Patch = newSubmission.Patch

		writes = append(writes, &comatproto.RepoApplyWrites_Input_Writes_Elem{
			RepoApplyWrites_Update: &comatproto.RepoApplyWrites_Update{
				Collection: tangled.RepoPullNSID,
				Rkey:       op.Rkey,
				Value: &lexutil.LexiconTypeDecoder{
					Val: &record,
				},
			},
		})
	}

	// update parent-change-id relations for the entire stack
	for _, p := range newStack {
		err := db.SetPullParentChangeId(
			tx,
			p.ParentChangeId,
			// these should be enough filters to be unique per-stack
			db.FilterEq("repo_at", p.RepoAt.String()),
			db.FilterEq("owner_did", p.OwnerDid),
			db.FilterEq("change_id", p.ChangeId),
		)

		if err != nil {
			log.Println("failed to update pull", err, p.PullId)
			s.pages.Notice(w, "pull-resubmit-error", "Failed to resubmit pull request. Try again later.")
			return
		}
	}

	err = tx.Commit()
	if err != nil {
		log.Println("failed to resubmit pull", err)
		s.pages.Notice(w, "pull-resubmit-error", "Failed to resubmit pull request. Try again later.")
		return
	}

	client, err := s.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to authorize client")
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	_, err = client.RepoApplyWrites(r.Context(), &comatproto.RepoApplyWrites_Input{
		Repo:   user.Did,
		Writes: writes,
	})
	if err != nil {
		log.Println("failed to create stacked pull request", err)
		s.pages.Notice(w, "pull", "Failed to create stacked pull request. Try again later.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
	return
}

func (s *Pulls) MergePull(w http.ResponseWriter, r *http.Request) {
	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to resolve repo:", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-merge-error", "Failed to merge patch. Try again later.")
		return
	}

	var pullsToMerge db.Stack
	pullsToMerge = append(pullsToMerge, pull)
	if pull.IsStacked() {
		stack, ok := r.Context().Value("stack").(db.Stack)
		if !ok {
			log.Println("failed to get stack")
			s.pages.Notice(w, "pull-merge-error", "Failed to merge patch. Try again later.")
			return
		}

		// combine patches of substack
		subStack := stack.StrictlyBelow(pull)
		// collect the portion of the stack that is mergeable
		mergeable := subStack.Mergeable()
		// add to total patch
		pullsToMerge = append(pullsToMerge, mergeable...)
	}

	patch := pullsToMerge.CombinedPatch()

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

	ksClient, err := knotclient.NewSignedClient(f.Knot, secret, s.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s: %s", f.Knot, err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	// Merge the pull request
	resp, err := ksClient.Merge([]byte(patch), f.OwnerDid(), f.RepoName, pull.TargetBranch, pull.Title, pull.Body, ident.Handle.String(), email.Address)
	if err != nil {
		log.Printf("failed to merge pull request: %s", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("knotserver returned non-OK status code for merge: %d", resp.StatusCode)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		log.Println("failed to start transcation", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}
	defer tx.Rollback()

	for _, p := range pullsToMerge {
		err := db.MergePull(tx, f.RepoAt, p.PullId)
		if err != nil {
			log.Printf("failed to update pull request status in database: %s", err)
			s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
			return
		}
	}

	err = tx.Commit()
	if err != nil {
		// TODO: this is unsound, we should also revert the merge from the knotserver here
		log.Printf("failed to update pull request status in database: %s", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s/pulls/%d", f.OwnerHandle(), f.RepoName, pull.PullId))
}

func (s *Pulls) ClosePull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	f, err := s.repoResolver.Resolve(r)
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
	roles := f.RolesInRepo(user)
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
	defer tx.Rollback()

	var pullsToClose []*db.Pull
	pullsToClose = append(pullsToClose, pull)

	// if this PR is stacked, then we want to close all PRs below this one on the stack
	if pull.IsStacked() {
		stack := r.Context().Value("stack").(db.Stack)
		subStack := stack.StrictlyBelow(pull)
		pullsToClose = append(pullsToClose, subStack...)
	}

	for _, p := range pullsToClose {
		// Close the pull in the database
		err = db.ClosePull(tx, f.RepoAt, p.PullId)
		if err != nil {
			log.Println("failed to close pull", err)
			s.pages.Notice(w, "pull-close", "Failed to close pull.")
			return
		}
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

func (s *Pulls) ReopenPull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	f, err := s.repoResolver.Resolve(r)
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
	roles := f.RolesInRepo(user)
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
	defer tx.Rollback()

	var pullsToReopen []*db.Pull
	pullsToReopen = append(pullsToReopen, pull)

	// if this PR is stacked, then we want to reopen all PRs above this one on the stack
	if pull.IsStacked() {
		stack := r.Context().Value("stack").(db.Stack)
		subStack := stack.StrictlyAbove(pull)
		pullsToReopen = append(pullsToReopen, subStack...)
	}

	for _, p := range pullsToReopen {
		// Close the pull in the database
		err = db.ReopenPull(tx, f.RepoAt, p.PullId)
		if err != nil {
			log.Println("failed to close pull", err)
			s.pages.Notice(w, "pull-close", "Failed to close pull.")
			return
		}
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

func newStack(f *reporesolver.ResolvedRepo, user *oauth.User, targetBranch, patch string, pullSource *db.PullSource, stackId string) (db.Stack, error) {
	formatPatches, err := patchutil.ExtractPatches(patch)
	if err != nil {
		return nil, fmt.Errorf("Failed to extract patches: %v", err)
	}

	//  must have atleast 1 patch to begin with
	if len(formatPatches) == 0 {
		return nil, fmt.Errorf("No patches found in the generated format-patch.")
	}

	// the stack is identified by a UUID
	var stack db.Stack
	parentChangeId := ""
	for _, fp := range formatPatches {
		//  all patches must have a jj change-id
		changeId, err := fp.ChangeId()
		if err != nil {
			return nil, fmt.Errorf("Stacking is only supported if all patches contain a change-id commit header.")
		}

		title := fp.Title
		body := fp.Body
		rkey := appview.TID()

		initialSubmission := db.PullSubmission{
			Patch:     fp.Raw,
			SourceRev: fp.SHA,
		}
		pull := db.Pull{
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
			Created:    time.Now(),

			StackId:        stackId,
			ChangeId:       changeId,
			ParentChangeId: parentChangeId,
		}

		stack = append(stack, &pull)

		parentChangeId = changeId
	}

	return stack, nil
}
