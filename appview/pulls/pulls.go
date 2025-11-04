package pulls

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	pulls_indexer "tangled.org/core/appview/indexer/pulls"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/appview/reporesolver"
	"tangled.org/core/appview/validator"
	"tangled.org/core/appview/xrpcclient"
	"tangled.org/core/idresolver"
	"tangled.org/core/patchutil"
	"tangled.org/core/rbac"
	"tangled.org/core/tid"
	"tangled.org/core/types"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Pulls struct {
	oauth        *oauth.OAuth
	repoResolver *reporesolver.RepoResolver
	pages        *pages.Pages
	idResolver   *idresolver.Resolver
	db           *db.DB
	config       *config.Config
	notifier     notify.Notifier
	enforcer     *rbac.Enforcer
	logger       *slog.Logger
	validator    *validator.Validator
	indexer      *pulls_indexer.Indexer
}

func New(
	oauth *oauth.OAuth,
	repoResolver *reporesolver.RepoResolver,
	pages *pages.Pages,
	resolver *idresolver.Resolver,
	db *db.DB,
	config *config.Config,
	notifier notify.Notifier,
	enforcer *rbac.Enforcer,
	validator *validator.Validator,
	indexer *pulls_indexer.Indexer,
	logger *slog.Logger,
) *Pulls {
	return &Pulls{
		oauth:        oauth,
		repoResolver: repoResolver,
		pages:        pages,
		idResolver:   resolver,
		db:           db,
		config:       config,
		notifier:     notifier,
		enforcer:     enforcer,
		logger:       logger,
		validator:    validator,
		indexer:      indexer,
	}
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

		pull, ok := r.Context().Value("pull").(*models.Pull)
		if !ok {
			log.Println("failed to get pull")
			s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
			return
		}

		// can be nil  if this pull is not stacked
		stack, _ := r.Context().Value("stack").(models.Stack)

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

		mergeCheckResponse := s.mergeCheck(r, f, pull, stack)
		branchDeleteStatus := s.branchDeleteStatus(r, f, pull)
		resubmitResult := pages.Unknown
		if user.Did == pull.OwnerDid {
			resubmitResult = s.resubmitCheck(r, f, pull, stack)
		}

		s.pages.PullActionsFragment(w, pages.PullActionsParams{
			LoggedInUser:       user,
			RepoInfo:           f.RepoInfo(user),
			Pull:               pull,
			RoundNumber:        roundNumber,
			MergeCheck:         mergeCheckResponse,
			ResubmitCheck:      resubmitResult,
			BranchDeleteStatus: branchDeleteStatus,
			Stack:              stack,
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

	pull, ok := r.Context().Value("pull").(*models.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// can be nil  if this pull is not stacked
	stack, _ := r.Context().Value("stack").(models.Stack)
	abandonedPulls, _ := r.Context().Value("abandonedPulls").([]*models.Pull)

	mergeCheckResponse := s.mergeCheck(r, f, pull, stack)
	branchDeleteStatus := s.branchDeleteStatus(r, f, pull)
	resubmitResult := pages.Unknown
	if user != nil && user.Did == pull.OwnerDid {
		resubmitResult = s.resubmitCheck(r, f, pull, stack)
	}

	repoInfo := f.RepoInfo(user)

	m := make(map[string]models.Pipeline)

	var shas []string
	for _, s := range pull.Submissions {
		shas = append(shas, s.SourceRev)
	}
	for _, p := range stack {
		shas = append(shas, p.LatestSha())
	}
	for _, p := range abandonedPulls {
		shas = append(shas, p.LatestSha())
	}

	ps, err := db.GetPipelineStatuses(
		s.db,
		db.FilterEq("repo_owner", repoInfo.OwnerDid),
		db.FilterEq("repo_name", repoInfo.Name),
		db.FilterEq("knot", repoInfo.Knot),
		db.FilterIn("sha", shas),
	)
	if err != nil {
		log.Printf("failed to fetch pipeline statuses: %s", err)
		// non-fatal
	}

	for _, p := range ps {
		m[p.Sha] = p
	}

	reactionMap, err := db.GetReactionMap(s.db, 20, pull.AtUri())
	if err != nil {
		log.Println("failed to get pull reactions")
		s.pages.Notice(w, "pulls", "Failed to load pull. Try again later.")
	}

	userReactions := map[models.ReactionKind]bool{}
	if user != nil {
		userReactions = db.GetReactionStatusMap(s.db, user.Did, pull.AtUri())
	}

	labelDefs, err := db.GetLabelDefinitions(
		s.db,
		db.FilterIn("at_uri", f.Repo.Labels),
		db.FilterContains("scope", tangled.RepoPullNSID),
	)
	if err != nil {
		log.Println("failed to fetch labels", err)
		s.pages.Error503(w)
		return
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labelDefs {
		defs[l.AtUri().String()] = &l
	}

	s.pages.RepoSinglePull(w, pages.RepoSinglePullParams{
		LoggedInUser:       user,
		RepoInfo:           repoInfo,
		Pull:               pull,
		Stack:              stack,
		AbandonedPulls:     abandonedPulls,
		BranchDeleteStatus: branchDeleteStatus,
		MergeCheck:         mergeCheckResponse,
		ResubmitCheck:      resubmitResult,
		Pipelines:          m,

		OrderedReactionKinds: models.OrderedReactionKinds,
		Reactions:            reactionMap,
		UserReacted:          userReactions,

		LabelDefs: defs,
	})
}

func (s *Pulls) mergeCheck(r *http.Request, f *reporesolver.ResolvedRepo, pull *models.Pull, stack models.Stack) types.MergeCheckResponse {
	if pull.State == models.PullMerged {
		return types.MergeCheckResponse{}
	}

	scheme := "https"
	if s.config.Core.Dev {
		scheme = "http"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)

	xrpcc := indigoxrpc.Client{
		Host: host,
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

	resp, xe := tangled.RepoMergeCheck(
		r.Context(),
		&xrpcc,
		&tangled.RepoMergeCheck_Input{
			Did:    f.OwnerDid(),
			Name:   f.Name,
			Branch: pull.TargetBranch,
			Patch:  patch,
		},
	)
	if err := xrpcclient.HandleXrpcErr(xe); err != nil {
		log.Println("failed to check for mergeability", "err", err)
		return types.MergeCheckResponse{
			Error: fmt.Sprintf("failed to check merge status: %s", err.Error()),
		}
	}

	// convert xrpc response to internal types
	conflicts := make([]types.ConflictInfo, len(resp.Conflicts))
	for i, conflict := range resp.Conflicts {
		conflicts[i] = types.ConflictInfo{
			Filename: conflict.Filename,
			Reason:   conflict.Reason,
		}
	}

	result := types.MergeCheckResponse{
		IsConflicted: resp.Is_conflicted,
		Conflicts:    conflicts,
	}

	if resp.Message != nil {
		result.Message = *resp.Message
	}

	if resp.Error != nil {
		result.Error = *resp.Error
	}

	return result
}

func (s *Pulls) branchDeleteStatus(r *http.Request, f *reporesolver.ResolvedRepo, pull *models.Pull) *models.BranchDeleteStatus {
	if pull.State != models.PullMerged {
		return nil
	}

	user := s.oauth.GetUser(r)
	if user == nil {
		return nil
	}

	var branch string
	var repo *models.Repo
	// check if the branch exists
	// NOTE: appview could cache branches/tags etc. for every repo by listening for gitRefUpdates
	if pull.IsBranchBased() {
		branch = pull.PullSource.Branch
		repo = &f.Repo
	} else if pull.IsForkBased() {
		branch = pull.PullSource.Branch
		repo = pull.PullSource.Repo
	} else {
		return nil
	}

	// deleted fork
	if repo == nil {
		return nil
	}

	// user can only delete branch if they are a collaborator in the repo that the branch belongs to
	perms := s.enforcer.GetPermissionsInRepo(user.Did, repo.Knot, repo.DidSlashRepo())
	if !slices.Contains(perms, "repo:push") {
		return nil
	}

	scheme := "http"
	if !s.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, repo.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	resp, err := tangled.RepoBranch(r.Context(), xrpcc, branch, fmt.Sprintf("%s/%s", repo.Did, repo.Name))
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		return nil
	}

	return &models.BranchDeleteStatus{
		Repo:   repo,
		Branch: resp.Name,
	}
}

func (s *Pulls) resubmitCheck(r *http.Request, f *reporesolver.ResolvedRepo, pull *models.Pull, stack models.Stack) pages.ResubmitResult {
	if pull.State == models.PullMerged || pull.State == models.PullDeleted || pull.PullSource == nil {
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
		repoName = f.Name
	}

	scheme := "http"
	if !s.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", ownerDid, repoName)
	branchResp, err := tangled.RepoBranch(r.Context(), xrpcc, pull.PullSource.Branch, repo)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.branches", xrpcerr)
			return pages.Unknown
		}
		log.Println("failed to reach knotserver", err)
		return pages.Unknown
	}

	targetBranch := branchResp

	latestSourceRev := pull.LatestSha()

	if pull.IsStacked() && stack != nil {
		top := stack[0]
		latestSourceRev = top.LatestSha()
	}

	if latestSourceRev != targetBranch.Hash {
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

	var diffOpts types.DiffOpts
	if d := r.URL.Query().Get("diff"); d == "split" {
		diffOpts.Split = true
	}

	pull, ok := r.Context().Value("pull").(*models.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	stack, _ := r.Context().Value("stack").(models.Stack)

	roundId := chi.URLParam(r, "round")
	roundIdInt, err := strconv.Atoi(roundId)
	if err != nil || roundIdInt >= len(pull.Submissions) {
		http.Error(w, "bad round id", http.StatusBadRequest)
		log.Println("failed to parse round id", err)
		return
	}

	patch := pull.Submissions[roundIdInt].CombinedPatch()
	diff := patchutil.AsNiceDiff(patch, pull.TargetBranch)

	s.pages.RepoPullPatchPage(w, pages.RepoPullPatchParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Pull:         pull,
		Stack:        stack,
		Round:        roundIdInt,
		Submission:   pull.Submissions[roundIdInt],
		Diff:         &diff,
		DiffOpts:     diffOpts,
	})

}

func (s *Pulls) RepoPullInterdiff(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	var diffOpts types.DiffOpts
	if d := r.URL.Query().Get("diff"); d == "split" {
		diffOpts.Split = true
	}

	pull, ok := r.Context().Value("pull").(*models.Pull)
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

	currentPatch, err := patchutil.AsDiff(pull.Submissions[roundIdInt].CombinedPatch())
	if err != nil {
		log.Println("failed to interdiff; current patch malformed")
		s.pages.Notice(w, fmt.Sprintf("interdiff-error-%d", roundIdInt), "Failed to calculate interdiff; current patch is invalid.")
		return
	}

	previousPatch, err := patchutil.AsDiff(pull.Submissions[roundIdInt-1].CombinedPatch())
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
		Interdiff:    interdiff,
		DiffOpts:     diffOpts,
	})
}

func (s *Pulls) RepoPullPatchRaw(w http.ResponseWriter, r *http.Request) {
	pull, ok := r.Context().Value("pull").(*models.Pull)
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

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(pull.Submissions[roundIdInt].Patch))
}

func (s *Pulls) RepoPulls(w http.ResponseWriter, r *http.Request) {
	l := s.logger.With("handler", "RepoPulls")

	user := s.oauth.GetUser(r)
	params := r.URL.Query()

	state := models.PullOpen
	switch params.Get("state") {
	case "closed":
		state = models.PullClosed
	case "merged":
		state = models.PullMerged
	}

	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	keyword := params.Get("q")

	var ids []int64
	searchOpts := models.PullSearchOptions{
		Keyword: keyword,
		RepoAt:  f.RepoAt().String(),
		State:   state,
		// Page: page,
	}
	l.Debug("searching with", "searchOpts", searchOpts)
	if keyword != "" {
		res, err := s.indexer.Search(r.Context(), searchOpts)
		if err != nil {
			l.Error("failed to search for pulls", "err", err)
			return
		}
		ids = res.Hits
		l.Debug("searched pulls with indexer", "count", len(ids))
	} else {
		ids, err = db.GetPullIDs(s.db, searchOpts)
		if err != nil {
			l.Error("failed to get all pull ids", "err", err)
			return
		}
		l.Debug("indexed all pulls from the db", "count", len(ids))
	}

	pulls, err := db.GetPulls(
		s.db,
		db.FilterIn("id", ids),
	)
	if err != nil {
		log.Println("failed to get pulls", err)
		s.pages.Notice(w, "pulls", "Failed to load pulls. Try again later.")
		return
	}

	for _, p := range pulls {
		var pullSourceRepo *models.Repo
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

	// we want to group all stacked PRs into just one list
	stacks := make(map[string]models.Stack)
	var shas []string
	n := 0
	for _, p := range pulls {
		// store the sha for later
		shas = append(shas, p.LatestSha())
		// this PR is stacked
		if p.StackId != "" {
			// we have already seen this PR stack
			if _, seen := stacks[p.StackId]; seen {
				stacks[p.StackId] = append(stacks[p.StackId], p)
				// skip this PR
			} else {
				stacks[p.StackId] = nil
				pulls[n] = p
				n++
			}
		} else {
			pulls[n] = p
			n++
		}
	}
	pulls = pulls[:n]

	repoInfo := f.RepoInfo(user)
	ps, err := db.GetPipelineStatuses(
		s.db,
		db.FilterEq("repo_owner", repoInfo.OwnerDid),
		db.FilterEq("repo_name", repoInfo.Name),
		db.FilterEq("knot", repoInfo.Knot),
		db.FilterIn("sha", shas),
	)
	if err != nil {
		log.Printf("failed to fetch pipeline statuses: %s", err)
		// non-fatal
	}
	m := make(map[string]models.Pipeline)
	for _, p := range ps {
		m[p.Sha] = p
	}

	labelDefs, err := db.GetLabelDefinitions(
		s.db,
		db.FilterIn("at_uri", f.Repo.Labels),
		db.FilterContains("scope", tangled.RepoPullNSID),
	)
	if err != nil {
		log.Println("failed to fetch labels", err)
		s.pages.Error503(w)
		return
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labelDefs {
		defs[l.AtUri().String()] = &l
	}

	s.pages.RepoPulls(w, pages.RepoPullsParams{
		LoggedInUser: s.oauth.GetUser(r),
		RepoInfo:     f.RepoInfo(user),
		Pulls:        pulls,
		LabelDefs:    defs,
		FilteringBy:  state,
		FilterQuery:  keyword,
		Stacks:       stacks,
		Pipelines:    m,
	})
}

func (s *Pulls) PullComment(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := r.Context().Value("pull").(*models.Pull)
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

		client, err := s.oauth.AuthorizedClient(r)
		if err != nil {
			log.Println("failed to get authorized client", err)
			s.pages.Notice(w, "pull-comment", "Failed to create comment.")
			return
		}
		atResp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoPullCommentNSID,
			Repo:       user.Did,
			Rkey:       tid.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoPullComment{
					Pull:      pull.AtUri().String(),
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

		comment := &models.PullComment{
			OwnerDid:     user.Did,
			RepoAt:       f.RepoAt().String(),
			PullId:       pull.PullId,
			Body:         body,
			CommentAt:    atResp.Uri,
			SubmissionId: pull.Submissions[roundNumber].ID,
		}

		// Create the pull comment in the database with the commentAt field
		commentId, err := db.NewPullComment(tx, comment)
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

		s.notifier.NewPullComment(r.Context(), comment)

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
		scheme := "http"
		if !s.config.Core.Dev {
			scheme = "https"
		}
		host := fmt.Sprintf("%s://%s", scheme, f.Knot)
		xrpcc := &indigoxrpc.Client{
			Host: host,
		}

		repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
		xrpcBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
		if err != nil {
			if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
				log.Println("failed to call XRPC repo.branches", xrpcerr)
				s.pages.Error503(w)
				return
			}
			log.Println("failed to fetch branches", err)
			return
		}

		var result types.RepoBranchesResponse
		if err := json.Unmarshal(xrpcBytes, &result); err != nil {
			log.Println("failed to decode XRPC response", err)
			s.pages.Error503(w)
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
			sanitizer := markup.NewSanitizer()
			if st := strings.TrimSpace(sanitizer.SanitizeDescription(title)); (st) == "" {
				s.pages.Notice(w, "pull", "Title is empty after HTML sanitization")
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

		// us, err := knotclient.NewUnsignedClient(f.Knot, s.config.Core.Dev)
		// if err != nil {
		// 	log.Printf("failed to create unsigned client to %s: %v", f.Knot, err)
		// 	s.pages.Notice(w, "pull", "Failed to create a pull request. Try again later.")
		// 	return
		// }

		// TODO: make capabilities an xrpc call
		caps := struct {
			PullRequests struct {
				FormatPatch       bool
				BranchSubmissions bool
				ForkSubmissions   bool
				PatchSubmissions  bool
			}
		}{
			PullRequests: struct {
				FormatPatch       bool
				BranchSubmissions bool
				ForkSubmissions   bool
				PatchSubmissions  bool
			}{
				FormatPatch:       true,
				BranchSubmissions: true,
				ForkSubmissions:   true,
				PatchSubmissions:  true,
			},
		}

		// caps, err := us.Capabilities()
		// if err != nil {
		// 	log.Println("error fetching knot caps", f.Knot, err)
		// 	s.pages.Notice(w, "pull", "Failed to create a pull request. Try again later.")
		// 	return
		// }

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
	scheme := "http"
	if !s.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcBytes, err := tangled.RepoCompare(r.Context(), xrpcc, repo, targetBranch, sourceBranch)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.compare", xrpcerr)
			s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
			return
		}
		log.Println("failed to compare", err)
		s.pages.Notice(w, "pull", err.Error())
		return
	}

	var comparison types.RepoFormatPatchResponse
	if err := json.Unmarshal(xrpcBytes, &comparison); err != nil {
		log.Println("failed to decode XRPC compare response", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.FormatPatchRaw
	combined := comparison.CombinedPatchRaw

	if err := s.validator.ValidatePatch(&patch); err != nil {
		s.logger.Error("failed to validate patch", "err", err)
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	pullSource := &models.PullSource{
		Branch: sourceBranch,
	}
	recordPullSource := &tangled.RepoPull_Source{
		Branch: sourceBranch,
		Sha:    comparison.Rev2,
	}

	s.createPullRequest(w, r, f, user, title, body, targetBranch, patch, combined, sourceRev, pullSource, recordPullSource, isStacked)
}

func (s *Pulls) handlePatchBasedPull(w http.ResponseWriter, r *http.Request, f *reporesolver.ResolvedRepo, user *oauth.User, title, body, targetBranch, patch string, isStacked bool) {
	if err := s.validator.ValidatePatch(&patch); err != nil {
		s.logger.Error("patch validation failed", "err", err)
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	s.createPullRequest(w, r, f, user, title, body, targetBranch, patch, "", "", nil, nil, isStacked)
}

func (s *Pulls) handleForkBasedPull(w http.ResponseWriter, r *http.Request, f *reporesolver.ResolvedRepo, user *oauth.User, forkRepo string, title, body, targetBranch, sourceBranch string, isStacked bool) {
	repoString := strings.SplitN(forkRepo, "/", 2)
	forkOwnerDid := repoString[0]
	repoName := repoString[1]
	fork, err := db.GetForkByDid(s.db, forkOwnerDid, repoName)
	if errors.Is(err, sql.ErrNoRows) {
		s.pages.Notice(w, "pull", "No such fork.")
		return
	} else if err != nil {
		log.Println("failed to fetch fork:", err)
		s.pages.Notice(w, "pull", "Failed to fetch fork.")
		return
	}

	client, err := s.oauth.ServiceClient(
		r,
		oauth.WithService(fork.Knot),
		oauth.WithLxm(tangled.RepoHiddenRefNSID),
		oauth.WithDev(s.config.Core.Dev),
	)

	resp, err := tangled.RepoHiddenRef(
		r.Context(),
		client,
		&tangled.RepoHiddenRef_Input{
			ForkRef:   sourceBranch,
			RemoteRef: targetBranch,
			Repo:      fork.RepoAt().String(),
		},
	)
	if err := xrpcclient.HandleXrpcErr(err); err != nil {
		s.pages.Notice(w, "pull", err.Error())
		return
	}

	if !resp.Success {
		errorMsg := "Failed to create pull request"
		if resp.Error != nil {
			errorMsg = fmt.Sprintf("Failed to create pull request: %s", *resp.Error)
		}
		s.pages.Notice(w, "pull", errorMsg)
		return
	}

	hiddenRef := fmt.Sprintf("hidden/%s/%s", sourceBranch, targetBranch)
	// We're now comparing the sourceBranch (on the fork) against the hiddenRef which is tracking
	// the targetBranch on the target repository. This code is a bit confusing, but here's an example:
	// hiddenRef: hidden/feature-1/main (on repo-fork)
	// targetBranch: main (on repo-1)
	// sourceBranch: feature-1 (on repo-fork)
	forkScheme := "http"
	if !s.config.Core.Dev {
		forkScheme = "https"
	}
	forkHost := fmt.Sprintf("%s://%s", forkScheme, fork.Knot)
	forkXrpcc := &indigoxrpc.Client{
		Host: forkHost,
	}

	forkRepoId := fmt.Sprintf("%s/%s", fork.Did, fork.Name)
	forkXrpcBytes, err := tangled.RepoCompare(r.Context(), forkXrpcc, forkRepoId, hiddenRef, sourceBranch)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.compare for fork", xrpcerr)
			s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
			return
		}
		log.Println("failed to compare across branches", err)
		s.pages.Notice(w, "pull", err.Error())
		return
	}

	var comparison types.RepoFormatPatchResponse
	if err := json.Unmarshal(forkXrpcBytes, &comparison); err != nil {
		log.Println("failed to decode XRPC compare response for fork", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.FormatPatchRaw
	combined := comparison.CombinedPatchRaw

	if err := s.validator.ValidatePatch(&patch); err != nil {
		s.logger.Error("failed to validate patch", "err", err)
		s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
		return
	}

	forkAtUri := fork.RepoAt()
	forkAtUriStr := forkAtUri.String()

	pullSource := &models.PullSource{
		Branch: sourceBranch,
		RepoAt: &forkAtUri,
	}
	recordPullSource := &tangled.RepoPull_Source{
		Branch: sourceBranch,
		Repo:   &forkAtUriStr,
		Sha:    sourceRev,
	}

	s.createPullRequest(w, r, f, user, title, body, targetBranch, patch, combined, sourceRev, pullSource, recordPullSource, isStacked)
}

func (s *Pulls) createPullRequest(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	title, body, targetBranch string,
	patch string,
	combined string,
	sourceRev string,
	pullSource *models.PullSource,
	recordPullSource *tangled.RepoPull_Source,
	isStacked bool,
) {
	if isStacked {
		// creates a series of PRs, each linking to the previous, identified by jj's change-id
		s.createStackedPullRequest(
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
	if title == "" || body == "" {
		formatPatches, err := patchutil.ExtractPatches(patch)
		if err != nil {
			s.pages.Notice(w, "pull", fmt.Sprintf("Failed to extract patches: %v", err))
			return
		}
		if len(formatPatches) == 0 {
			s.pages.Notice(w, "pull", "No patches found in the supplied format-patch.")
			return
		}

		if title == "" {
			title = formatPatches[0].Title
		}
		if body == "" {
			body = formatPatches[0].Body
		}
	}

	rkey := tid.TID()
	initialSubmission := models.PullSubmission{
		Patch:     patch,
		Combined:  combined,
		SourceRev: sourceRev,
	}
	pull := &models.Pull{
		Title:        title,
		Body:         body,
		TargetBranch: targetBranch,
		OwnerDid:     user.Did,
		RepoAt:       f.RepoAt(),
		Rkey:         rkey,
		Submissions: []*models.PullSubmission{
			&initialSubmission,
		},
		PullSource: pullSource,
	}
	err = db.NewPull(tx, pull)
	if err != nil {
		log.Println("failed to create pull request", err)
		s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
		return
	}
	pullId, err := db.NextPullId(tx, f.RepoAt())
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
				Title: title,
				Target: &tangled.RepoPull_Target{
					Repo:   string(f.RepoAt()),
					Branch: targetBranch,
				},
				Patch:     patch,
				Source:    recordPullSource,
				CreatedAt: time.Now().Format(time.RFC3339),
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

	s.notifier.NewPull(r.Context(), pull)

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pullId))
}

func (s *Pulls) createStackedPullRequest(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	targetBranch string,
	patch string,
	sourceRev string,
	pullSource *models.PullSource,
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
	_, err = comatproto.RepoApplyWrites(r.Context(), client, &comatproto.RepoApplyWrites_Input{
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

	if err := s.validator.ValidatePatch(&patch); err != nil {
		s.logger.Error("faield to validate patch", "err", err)
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

	scheme := "http"
	if !s.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.branches", xrpcerr)
			s.pages.Error503(w)
			return
		}
		log.Println("failed to fetch branches", err)
		return
	}

	var result types.RepoBranchesResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		log.Println("failed to decode XRPC response", err)
		s.pages.Error503(w)
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
	repoString := strings.SplitN(forkVal, "/", 2)
	forkOwnerDid := repoString[0]
	forkName := repoString[1]
	// fork repo
	repo, err := db.GetRepo(
		s.db,
		db.FilterEq("did", forkOwnerDid),
		db.FilterEq("name", forkName),
	)
	if err != nil {
		log.Println("failed to get repo", "did", forkOwnerDid, "name", forkName, "err", err)
		return
	}

	sourceScheme := "http"
	if !s.config.Core.Dev {
		sourceScheme = "https"
	}
	sourceHost := fmt.Sprintf("%s://%s", sourceScheme, repo.Knot)
	sourceXrpcc := &indigoxrpc.Client{
		Host: sourceHost,
	}

	sourceRepo := fmt.Sprintf("%s/%s", forkOwnerDid, repo.Name)
	sourceXrpcBytes, err := tangled.RepoBranches(r.Context(), sourceXrpcc, "", 0, sourceRepo)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.branches for source", xrpcerr)
			s.pages.Error503(w)
			return
		}
		log.Println("failed to fetch source branches", err)
		return
	}

	// Decode source branches
	var sourceBranches types.RepoBranchesResponse
	if err := json.Unmarshal(sourceXrpcBytes, &sourceBranches); err != nil {
		log.Println("failed to decode source branches XRPC response", err)
		s.pages.Error503(w)
		return
	}

	targetScheme := "http"
	if !s.config.Core.Dev {
		targetScheme = "https"
	}
	targetHost := fmt.Sprintf("%s://%s", targetScheme, f.Knot)
	targetXrpcc := &indigoxrpc.Client{
		Host: targetHost,
	}

	targetRepo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	targetXrpcBytes, err := tangled.RepoBranches(r.Context(), targetXrpcc, "", 0, targetRepo)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.branches for target", xrpcerr)
			s.pages.Error503(w)
			return
		}
		log.Println("failed to fetch target branches", err)
		return
	}

	// Decode target branches
	var targetBranches types.RepoBranchesResponse
	if err := json.Unmarshal(targetXrpcBytes, &targetBranches); err != nil {
		log.Println("failed to decode target branches XRPC response", err)
		s.pages.Error503(w)
		return
	}

	sort.Slice(sourceBranches.Branches, func(i int, j int) bool {
		return sourceBranches.Branches[i].Commit.Committer.When.After(sourceBranches.Branches[j].Commit.Committer.When)
	})

	s.pages.PullCompareForkBranchesFragment(w, pages.PullCompareForkBranchesParams{
		RepoInfo:       f.RepoInfo(user),
		SourceBranches: sourceBranches.Branches,
		TargetBranches: targetBranches.Branches,
	})
}

func (s *Pulls) ResubmitPull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok := r.Context().Value("pull").(*models.Pull)
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

	pull, ok := r.Context().Value("pull").(*models.Pull)
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

	s.resubmitPullHelper(w, r, f, user, pull, patch, "", "")
}

func (s *Pulls) resubmitBranch(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	pull, ok := r.Context().Value("pull").(*models.Pull)
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

	scheme := "http"
	if !s.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcBytes, err := tangled.RepoCompare(r.Context(), xrpcc, repo, pull.TargetBranch, pull.PullSource.Branch)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.compare", xrpcerr)
			s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
			return
		}
		log.Printf("compare request failed: %s", err)
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	var comparison types.RepoFormatPatchResponse
	if err := json.Unmarshal(xrpcBytes, &comparison); err != nil {
		log.Println("failed to decode XRPC compare response", err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	sourceRev := comparison.Rev2
	patch := comparison.FormatPatchRaw
	combined := comparison.CombinedPatchRaw

	s.resubmitPullHelper(w, r, f, user, pull, patch, combined, sourceRev)
}

func (s *Pulls) resubmitFork(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	pull, ok := r.Context().Value("pull").(*models.Pull)
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

	// update the hidden tracking branch to latest
	client, err := s.oauth.ServiceClient(
		r,
		oauth.WithService(forkRepo.Knot),
		oauth.WithLxm(tangled.RepoHiddenRefNSID),
		oauth.WithDev(s.config.Core.Dev),
	)
	if err != nil {
		log.Printf("failed to connect to knot server: %v", err)
		return
	}

	resp, err := tangled.RepoHiddenRef(
		r.Context(),
		client,
		&tangled.RepoHiddenRef_Input{
			ForkRef:   pull.PullSource.Branch,
			RemoteRef: pull.TargetBranch,
			Repo:      forkRepo.RepoAt().String(),
		},
	)
	if err := xrpcclient.HandleXrpcErr(err); err != nil {
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}
	if !resp.Success {
		log.Println("Failed to update tracking ref.", "err", resp.Error)
		s.pages.Notice(w, "resubmit-error", "Failed to update tracking ref.")
		return
	}

	hiddenRef := fmt.Sprintf("hidden/%s/%s", pull.PullSource.Branch, pull.TargetBranch)
	// extract patch by performing compare
	forkScheme := "http"
	if !s.config.Core.Dev {
		forkScheme = "https"
	}
	forkHost := fmt.Sprintf("%s://%s", forkScheme, forkRepo.Knot)
	forkRepoId := fmt.Sprintf("%s/%s", forkRepo.Did, forkRepo.Name)
	forkXrpcBytes, err := tangled.RepoCompare(r.Context(), &indigoxrpc.Client{Host: forkHost}, forkRepoId, hiddenRef, pull.PullSource.Branch)
	if err != nil {
		if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
			log.Println("failed to call XRPC repo.compare for fork", xrpcerr)
			s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
			return
		}
		log.Printf("failed to compare branches: %s", err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	var forkComparison types.RepoFormatPatchResponse
	if err := json.Unmarshal(forkXrpcBytes, &forkComparison); err != nil {
		log.Println("failed to decode XRPC compare response for fork", err)
		s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
		return
	}

	// Use the fork comparison we already made
	comparison := forkComparison

	sourceRev := comparison.Rev2
	patch := comparison.FormatPatchRaw
	combined := comparison.CombinedPatchRaw

	s.resubmitPullHelper(w, r, f, user, pull, patch, combined, sourceRev)
}

func (s *Pulls) resubmitPullHelper(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	pull *models.Pull,
	patch string,
	combined string,
	sourceRev string,
) {
	if pull.IsStacked() {
		log.Println("resubmitting stacked PR")
		s.resubmitStackedPullHelper(w, r, f, user, pull, patch, pull.StackId)
		return
	}

	if err := s.validator.ValidatePatch(&patch); err != nil {
		s.pages.Notice(w, "resubmit-error", err.Error())
		return
	}

	if patch == pull.LatestPatch() {
		s.pages.Notice(w, "resubmit-error", "Patch is identical to previous submission.")
		return
	}

	// validate sourceRev if branch/fork based
	if pull.IsBranchBased() || pull.IsForkBased() {
		if sourceRev == pull.LatestSha() {
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

	pullAt := pull.AtUri()
	newRoundNumber := len(pull.Submissions)
	newPatch := patch
	newSourceRev := sourceRev
	combinedPatch := combined
	err = db.ResubmitPull(tx, pullAt, newRoundNumber, newPatch, combinedPatch, newSourceRev)
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

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoPullNSID, user.Did, pull.Rkey)
	if err != nil {
		// failed to get record
		s.pages.Notice(w, "resubmit-error", "Failed to update pull, no record found on PDS.")
		return
	}

	var recordPullSource *tangled.RepoPull_Source
	if pull.IsBranchBased() {
		recordPullSource = &tangled.RepoPull_Source{
			Branch: pull.PullSource.Branch,
			Sha:    sourceRev,
		}
	}
	if pull.IsForkBased() {
		repoAt := pull.PullSource.RepoAt.String()
		recordPullSource = &tangled.RepoPull_Source{
			Branch: pull.PullSource.Branch,
			Repo:   &repoAt,
			Sha:    sourceRev,
		}
	}

	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoPullNSID,
		Repo:       user.Did,
		Rkey:       pull.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoPull{
				Title: pull.Title,
				Target: &tangled.RepoPull_Target{
					Repo:   string(f.RepoAt()),
					Branch: pull.TargetBranch,
				},
				Patch:     patch, // new patch
				Source:    recordPullSource,
				CreatedAt: time.Now().Format(time.RFC3339),
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
}

func (s *Pulls) resubmitStackedPullHelper(
	w http.ResponseWriter,
	r *http.Request,
	f *reporesolver.ResolvedRepo,
	user *oauth.User,
	pull *models.Pull,
	patch string,
	stackId string,
) {
	targetBranch := pull.TargetBranch

	origStack, _ := r.Context().Value("stack").(models.Stack)
	newStack, err := newStack(f, user, targetBranch, patch, pull.PullSource, stackId)
	if err != nil {
		log.Println("failed to create resubmitted stack", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	// find the diff between the stacks, first, map them by changeId
	origById := make(map[string]*models.Pull)
	newById := make(map[string]*models.Pull)
	for _, p := range origStack {
		origById[p.ChangeId] = p
	}
	for _, p := range newStack {
		newById[p.ChangeId] = p
	}

	// commits that got deleted: corresponding pull is closed
	// commits that got added: new pull is created
	// commits that got updated: corresponding pull is resubmitted & new round begins
	additions := make(map[string]*models.Pull)
	deletions := make(map[string]*models.Pull)
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
			updated[op.ChangeId] = struct{}{}
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
		// do not do delete already merged PRs
		if p.State == models.PullMerged {
			continue
		}

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

		// do not update already merged PRs
		if op.State == models.PullMerged {
			continue
		}

		// resubmit the new pull
		pullAt := op.AtUri()
		newRoundNumber := len(op.Submissions)
		newPatch := np.LatestPatch()
		combinedPatch := np.LatestSubmission().Combined
		newSourceRev := np.LatestSha()
		err := db.ResubmitPull(tx, pullAt, newRoundNumber, newPatch, combinedPatch, newSourceRev)
		if err != nil {
			log.Println("failed to update pull", err, op.PullId)
			s.pages.Notice(w, "pull-resubmit-error", "Failed to resubmit pull request. Try again later.")
			return
		}

		record := np.AsRecord()

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

	_, err = comatproto.RepoApplyWrites(r.Context(), client, &comatproto.RepoApplyWrites_Input{
		Repo:   user.Did,
		Writes: writes,
	})
	if err != nil {
		log.Println("failed to create stacked pull request", err)
		s.pages.Notice(w, "pull", "Failed to create stacked pull request. Try again later.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
}

func (s *Pulls) MergePull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to resolve repo:", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	pull, ok := r.Context().Value("pull").(*models.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-merge-error", "Failed to merge patch. Try again later.")
		return
	}

	var pullsToMerge models.Stack
	pullsToMerge = append(pullsToMerge, pull)
	if pull.IsStacked() {
		stack, ok := r.Context().Value("stack").(models.Stack)
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

	ident, err := s.idResolver.ResolveIdent(r.Context(), pull.OwnerDid)
	if err != nil {
		log.Printf("resolving identity: %s", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	email, err := db.GetPrimaryEmail(s.db, pull.OwnerDid)
	if err != nil {
		log.Printf("failed to get primary email: %s", err)
	}

	authorName := ident.Handle.String()
	mergeInput := &tangled.RepoMerge_Input{
		Did:           f.OwnerDid(),
		Name:          f.Name,
		Branch:        pull.TargetBranch,
		Patch:         patch,
		CommitMessage: &pull.Title,
		AuthorName:    &authorName,
	}

	if pull.Body != "" {
		mergeInput.CommitBody = &pull.Body
	}

	if email.Address != "" {
		mergeInput.AuthorEmail = &email.Address
	}

	client, err := s.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoMergeNSID),
		oauth.WithDev(s.config.Core.Dev),
	)
	if err != nil {
		log.Printf("failed to connect to knot server: %v", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	err = tangled.RepoMerge(r.Context(), client, mergeInput)
	if err := xrpcclient.HandleXrpcErr(err); err != nil {
		s.pages.Notice(w, "pull-merge-error", err.Error())
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
		err := db.MergePull(tx, f.RepoAt(), p.PullId)
		if err != nil {
			log.Printf("failed to update pull request status in database: %s", err)
			s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
			return
		}
		p.State = models.PullMerged
	}

	err = tx.Commit()
	if err != nil {
		// TODO: this is unsound, we should also revert the merge from the knotserver here
		log.Printf("failed to update pull request status in database: %s", err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	// notify about the pull merge
	for _, p := range pullsToMerge {
		s.notifier.NewPullState(r.Context(), syntax.DID(user.Did), p)
	}

	s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s/pulls/%d", f.OwnerHandle(), f.Name, pull.PullId))
}

func (s *Pulls) ClosePull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("malformed middleware")
		return
	}

	pull, ok := r.Context().Value("pull").(*models.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// auth filter: only owner or collaborators can close
	roles := f.RolesInRepo(user)
	isOwner := roles.IsOwner()
	isCollaborator := roles.IsCollaborator()
	isPullAuthor := user.Did == pull.OwnerDid
	isCloseAllowed := isOwner || isCollaborator || isPullAuthor
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

	var pullsToClose []*models.Pull
	pullsToClose = append(pullsToClose, pull)

	// if this PR is stacked, then we want to close all PRs below this one on the stack
	if pull.IsStacked() {
		stack := r.Context().Value("stack").(models.Stack)
		subStack := stack.StrictlyBelow(pull)
		pullsToClose = append(pullsToClose, subStack...)
	}

	for _, p := range pullsToClose {
		// Close the pull in the database
		err = db.ClosePull(tx, f.RepoAt(), p.PullId)
		if err != nil {
			log.Println("failed to close pull", err)
			s.pages.Notice(w, "pull-close", "Failed to close pull.")
			return
		}
		p.State = models.PullClosed
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		s.pages.Notice(w, "pull-close", "Failed to close pull.")
		return
	}

	for _, p := range pullsToClose {
		s.notifier.NewPullState(r.Context(), syntax.DID(user.Did), p)
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
}

func (s *Pulls) ReopenPull(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	f, err := s.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to resolve repo", err)
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	pull, ok := r.Context().Value("pull").(*models.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// auth filter: only owner or collaborators can close
	roles := f.RolesInRepo(user)
	isOwner := roles.IsOwner()
	isCollaborator := roles.IsCollaborator()
	isPullAuthor := user.Did == pull.OwnerDid
	isCloseAllowed := isOwner || isCollaborator || isPullAuthor
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

	var pullsToReopen []*models.Pull
	pullsToReopen = append(pullsToReopen, pull)

	// if this PR is stacked, then we want to reopen all PRs above this one on the stack
	if pull.IsStacked() {
		stack := r.Context().Value("stack").(models.Stack)
		subStack := stack.StrictlyAbove(pull)
		pullsToReopen = append(pullsToReopen, subStack...)
	}

	for _, p := range pullsToReopen {
		// Close the pull in the database
		err = db.ReopenPull(tx, f.RepoAt(), p.PullId)
		if err != nil {
			log.Println("failed to close pull", err)
			s.pages.Notice(w, "pull-close", "Failed to close pull.")
			return
		}
		p.State = models.PullOpen
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		log.Println("failed to commit transaction", err)
		s.pages.Notice(w, "pull-reopen", "Failed to reopen pull.")
		return
	}

	for _, p := range pullsToReopen {
		s.notifier.NewPullState(r.Context(), syntax.DID(user.Did), p)
	}

	s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pull.PullId))
}

func newStack(f *reporesolver.ResolvedRepo, user *oauth.User, targetBranch, patch string, pullSource *models.PullSource, stackId string) (models.Stack, error) {
	formatPatches, err := patchutil.ExtractPatches(patch)
	if err != nil {
		return nil, fmt.Errorf("Failed to extract patches: %v", err)
	}

	//  must have atleast 1 patch to begin with
	if len(formatPatches) == 0 {
		return nil, fmt.Errorf("No patches found in the generated format-patch.")
	}

	// the stack is identified by a UUID
	var stack models.Stack
	parentChangeId := ""
	for _, fp := range formatPatches {
		//  all patches must have a jj change-id
		changeId, err := fp.ChangeId()
		if err != nil {
			return nil, fmt.Errorf("Stacking is only supported if all patches contain a change-id commit header.")
		}

		title := fp.Title
		body := fp.Body
		rkey := tid.TID()

		initialSubmission := models.PullSubmission{
			Patch:     fp.Raw,
			SourceRev: fp.SHA,
			Combined:  fp.Raw,
		}
		pull := models.Pull{
			Title:        title,
			Body:         body,
			TargetBranch: targetBranch,
			OwnerDid:     user.Did,
			RepoAt:       f.RepoAt(),
			Rkey:         rkey,
			Submissions: []*models.PullSubmission{
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
