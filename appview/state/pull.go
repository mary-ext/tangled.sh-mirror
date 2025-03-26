package state

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/types"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
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

		s.pages.PullActionsFragment(w, pages.PullActionsParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(s, user),
			Pull:         pull,
			RoundNumber:  roundNumber,
			MergeCheck:   mergeCheckResponse,
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

	s.pages.RepoSinglePull(w, pages.RepoSinglePullParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(s, user),
		DidHandleMap: didHandleMap,
		Pull:         *pull,
		MergeCheck:   mergeCheckResponse,
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

	s.pages.RepoPullPatchPage(w, pages.RepoPullPatchParams{
		LoggedInUser: user,
		DidHandleMap: didHandleMap,
		RepoInfo:     f.RepoInfo(s, user),
		Pull:         pull,
		Round:        roundIdInt,
		Submission:   pull.Submissions[roundIdInt],
		Diff:         pull.Submissions[roundIdInt].AsNiceDiff(pull.TargetBranch),
	})

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
			Rkey:       s.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoPullComment{
					Repo:      &atUri,
					Pull:      pullAt,
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
		patch := r.FormValue("patch")

		if title == "" || body == "" || patch == "" || targetBranch == "" {
			s.pages.Notice(w, "pull", "Title, body and patch diff are required.")
			return
		}

		// Validate patch format
		if !isPatchValid(patch) {
			s.pages.Notice(w, "pull", "Invalid patch format. Please provide a valid diff.")
			return
		}

		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Println("failed to start tx")
			s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
			return
		}
		defer tx.Rollback()

		rkey := s.TID()
		initialSubmission := db.PullSubmission{
			Patch: patch,
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

		atResp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
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
				},
			},
		})

		err = db.SetPullAt(s.db, f.RepoAt, pullId, atResp.Uri)
		if err != nil {
			log.Println("failed to get pull id", err)
			s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d", f.OwnerSlashRepo(), pullId))
		return
	}
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
		patch := r.FormValue("patch")

		if patch == "" {
			s.pages.Notice(w, "resubmit-error", "Patch is empty.")
			return
		}

		if patch == pull.LatestPatch() {
			s.pages.Notice(w, "resubmit-error", "Patch is identical to previous submission.")
			return
		}

		// Validate patch format
		if !isPatchValid(patch) {
			s.pages.Notice(w, "resubmit-error", "Invalid patch format. Please provide a valid diff.")
			return
		}

		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Println("failed to start tx")
			s.pages.Notice(w, "resubmit-error", "Failed to create pull request. Try again later.")
			return
		}
		defer tx.Rollback()

		err = db.ResubmitPull(tx, pull, patch)
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

// Very basic validation to check if it looks like a diff/patch
// A valid patch usually starts with diff or --- lines
func isPatchValid(patch string) bool {
	// Basic validation to check if it looks like a diff/patch
	// A valid patch usually starts with diff or --- lines
	if len(patch) == 0 {
		return false
	}

	lines := strings.Split(patch, "\n")
	if len(lines) < 2 {
		return false
	}

	// Check for common patch format markers
	firstLine := strings.TrimSpace(lines[0])
	return strings.HasPrefix(firstLine, "diff ") ||
		strings.HasPrefix(firstLine, "--- ") ||
		strings.HasPrefix(firstLine, "Index: ") ||
		strings.HasPrefix(firstLine, "+++ ") ||
		strings.HasPrefix(firstLine, "@@ ")
}
