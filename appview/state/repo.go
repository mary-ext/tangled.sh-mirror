package state

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/appview/auth"
	"github.com/sotangled/tangled/appview/db"
	"github.com/sotangled/tangled/appview/pages"
	"github.com/sotangled/tangled/types"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

func (s *State) RepoIndex(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		return
	}

	us, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", f.Knot)
		s.pages.Error503(w)
		return
	}

	resp, err := us.Index(f.OwnerDid(), f.RepoName, ref)
	if err != nil {
		s.pages.Error503(w)
		log.Println("failed to reach knotserver", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	var result types.RepoIndexResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Printf("Error unmarshalling response body: %v", err)
		return
	}

	tagMap := make(map[string][]string)
	for _, tag := range result.Tags {
		hash := tag.Hash
		tagMap[hash] = append(tagMap[hash], tag.Name)
	}

	for _, branch := range result.Branches {
		hash := branch.Hash
		tagMap[hash] = append(tagMap[hash], branch.Name)
	}

	user := s.auth.GetUser(r)
	s.pages.RepoIndexPage(w, pages.RepoIndexParams{
		LoggedInUser:      user,
		RepoInfo:          f.RepoInfo(s, user),
		TagMap:            tagMap,
		RepoIndexResponse: result,
	})

	return
}

func (s *State) RepoLog(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		return
	}

	page := 1
	if r.URL.Query().Get("page") != "" {
		page, err = strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			page = 1
		}
	}

	ref := chi.URLParam(r, "ref")

	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}

	resp, err := http.Get(fmt.Sprintf("%s://%s/%s/%s/log/%s?page=%d&per_page=30", protocol, f.Knot, f.OwnerDid(), f.RepoName, ref, page))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body: %v", err)
		return
	}

	var repolog types.RepoLogResponse
	err = json.Unmarshal(body, &repolog)
	if err != nil {
		log.Println("failed to parse json response", err)
		return
	}

	user := s.auth.GetUser(r)
	s.pages.RepoLog(w, pages.RepoLogParams{
		LoggedInUser:    user,
		RepoInfo:        f.RepoInfo(s, user),
		RepoLogResponse: repolog,
	})
	return
}

func (s *State) RepoDescriptionEdit(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	user := s.auth.GetUser(r)
	s.pages.EditRepoDescriptionFragment(w, pages.RepoDescriptionParams{
		RepoInfo: f.RepoInfo(s, user),
	})
	return
}

func (s *State) RepoDescription(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	repoAt := f.RepoAt
	rkey := repoAt.RecordKey().String()
	if rkey == "" {
		log.Println("invalid aturi for repo", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	user := s.auth.GetUser(r)

	switch r.Method {
	case http.MethodGet:
		s.pages.RepoDescriptionFragment(w, pages.RepoDescriptionParams{
			RepoInfo: f.RepoInfo(s, user),
		})
		return
	case http.MethodPut:
		user := s.auth.GetUser(r)
		newDescription := r.FormValue("description")
		client, _ := s.auth.AuthorizedClient(r)

		// optimistic update
		err = db.UpdateDescription(s.db, string(repoAt), newDescription)
		if err != nil {
			log.Println("failed to perferom update-description query", err)
			s.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// this is a bit of a pain because the golang atproto impl does not allow nil SwapRecord field
		//
		// SwapRecord is optional and should happen automagically, but given that it does not, we have to perform two requests
		ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, user.Did, rkey)
		if err != nil {
			// failed to get record
			s.pages.Notice(w, "repo-notice", "Failed to update description, no record found on PDS.")
			return
		}
		_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			SwapRecord: ex.Cid,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.Repo{
					Knot:        f.Knot,
					Name:        f.RepoName,
					Owner:       user.Did,
					AddedAt:     &f.AddedAt,
					Description: &newDescription,
				},
			},
		})

		if err != nil {
			log.Println("failed to perferom update-description query", err)
			// failed to get record
			s.pages.Notice(w, "repo-notice", "Failed to update description, unable to save to PDS.")
			return
		}

		newRepoInfo := f.RepoInfo(s, user)
		newRepoInfo.Description = newDescription

		s.pages.RepoDescriptionFragment(w, pages.RepoDescriptionParams{
			RepoInfo: newRepoInfo,
		})
		return
	}
}

func (s *State) EditPatch(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	patch := r.FormValue("patch")
	if patch == "" {
		s.pages.Notice(w, "pull-error", "Patch is required.")
		return
	}

	pull, ok := r.Context().Value("pull").(*db.Pull)
	if !ok {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	if pull.OwnerDid != user.Did {
		log.Println("failed to edit pull information")
		s.pages.Notice(w, "pull-error", "Unauthorized")
		return
	}

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// Start a transaction for database operations
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start transaction", err)
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// Set up deferred rollback that will be overridden by commit if successful
	defer tx.Rollback()

	// Update patch in the database within transaction
	err = db.EditPatch(tx, f.RepoAt, pull.PullId, patch)
	if err != nil {
		log.Println("failed to update patch", err)
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// Update the atproto record
	client, _ := s.auth.AuthorizedClient(r)
	pullAt := pull.PullAt

	// Get the existing record first
	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoPullNSID, user.Did, pullAt.RecordKey().String())
	if err != nil {
		log.Println("failed to get existing pull record", err)
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// Update the record
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoPullNSID,
		Repo:       user.Did,
		Rkey:       pullAt.RecordKey().String(),
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoPull{
				Title:        pull.Title,
				PullId:       int64(pull.PullId),
				TargetRepo:   string(f.RepoAt),
				TargetBranch: pull.TargetBranch,
				Patch:        patch,
			},
		},
	})

	if err != nil {
		log.Println("failed to update pull record in atproto", err)
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	// Commit the transaction now that both operations have succeeded
	err = tx.Commit()
	if err != nil {
		log.Println("failed to commit transaction", err)
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	targetBranch := pull.TargetBranch

	// Perform merge check
	secret, err := db.GetRegistrationKey(s.db, f.Knot)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", f.Knot, err)
		s.pages.Notice(w, "pull-success", "Patch updated successfully, but couldn't check mergeability.")
		return
	}

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s", f.Knot)
		s.pages.Notice(w, "pull-success", "Patch updated successfully, but couldn't check mergeability.")
		return
	}

	resp, err := ksClient.MergeCheck([]byte(patch), user.Did, f.RepoName, targetBranch)
	if err != nil {
		log.Println("failed to check mergeability", err)
		s.pages.Notice(w, "pull-success", "Patch updated successfully, but couldn't check mergeability.")
		return
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("failed to read knotserver response body")
		s.pages.Notice(w, "pull-success", "Patch updated successfully, but couldn't check mergeability.")
		return
	}

	var mergeCheckResponse types.MergeCheckResponse
	err = json.Unmarshal(respBody, &mergeCheckResponse)
	if err != nil {
		log.Println("failed to unmarshal merge check response", err)
		s.pages.Notice(w, "pull-success", "Patch updated successfully, but couldn't check mergeability.")
		return
	}

	s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s/pulls/%d", f.OwnerHandle(), f.RepoName, pull.PullId))
	return
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

		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Println("failed to start tx")
			s.pages.Notice(w, "pull", "Failed to create pull request. Try again later.")
			return
		}

		defer func() {
			tx.Rollback()
			err = s.enforcer.E.LoadPolicy()
			if err != nil {
				log.Println("failed to rollback policies")
			}
		}()

		err = db.NewPull(tx, &db.Pull{
			Title:        title,
			Body:         body,
			TargetBranch: targetBranch,
			Patch:        patch,
			OwnerDid:     user.Did,
			RepoAt:       f.RepoAt,
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
			Rkey:       s.TID(),
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

func (s *State) RepoSinglePull(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pull, ok1 := r.Context().Value("pull").(*db.Pull)
	comments, ok2 := r.Context().Value("pull_comments").([]db.PullComment)
	if !ok1 || !ok2 {
		log.Println("failed to get pull")
		s.pages.Notice(w, "pull-error", "Failed to edit patch. Try again later.")
		return
	}

	identsToResolve := make([]string, len(comments))
	for i, comment := range comments {
		identsToResolve[i] = comment.OwnerDid
	}
	identsToResolve = append(identsToResolve, pull.OwnerDid)

	resolvedIds := s.resolver.ResolveIdents(r.Context(), identsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	var mergeCheckResponse types.MergeCheckResponse

	// Only perform merge check if the pull request is not already merged
	if pull.State != db.PullMerged {
		secret, err := db.GetRegistrationKey(s.db, f.Knot)
		if err != nil {
			log.Printf("failed to get registration key for %s", f.Knot)
			s.pages.Notice(w, "pull", "Failed to load pull request. Try again later.")
			return
		}

		ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
		if err == nil {
			resp, err := ksClient.MergeCheck([]byte(pull.Patch), pull.OwnerDid, f.RepoName, pull.TargetBranch)
			if err != nil {
				log.Println("failed to check for mergeability:", err)
			} else {
				respBody, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Println("failed to read merge check response body")
				} else {
					err = json.Unmarshal(respBody, &mergeCheckResponse)
					if err != nil {
						log.Println("failed to unmarshal merge check response", err)
					}
				}
			}
		} else {
			log.Printf("failed to setup signed client for %s; ignoring...", f.Knot)
		}
	}

	s.pages.RepoSinglePull(w, pages.RepoSinglePullParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(s, user),
		Pull:         *pull,
		Comments:     comments,
		DidHandleMap: didHandleMap,
		MergeCheck:   mergeCheckResponse,
	})
}

func (s *State) RepoCommit(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		return
	}
	ref := chi.URLParam(r, "ref")
	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}
	resp, err := http.Get(fmt.Sprintf("%s://%s/%s/%s/commit/%s", protocol, f.Knot, f.OwnerDid(), f.RepoName, ref))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	var result types.RepoCommitResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		return
	}

	user := s.auth.GetUser(r)
	s.pages.RepoCommit(w, pages.RepoCommitParams{
		LoggedInUser:       user,
		RepoInfo:           f.RepoInfo(s, user),
		RepoCommitResponse: result,
	})
	return
}

func (s *State) RepoTree(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	treePath := chi.URLParam(r, "*")
	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}
	resp, err := http.Get(fmt.Sprintf("%s://%s/%s/%s/tree/%s/%s", protocol, f.Knot, f.OwnerDid(), f.RepoName, ref, treePath))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	var result types.RepoTreeResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		return
	}

	user := s.auth.GetUser(r)

	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.RepoName, fmt.Sprintf("/%s/%s/tree/%s", f.OwnerDid(), f.RepoName, ref)})
	if treePath != "" {
		for idx, elem := range strings.Split(treePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], elem)})
		}
	}

	baseTreeLink := path.Join(f.OwnerDid(), f.RepoName, "tree", ref, treePath)
	baseBlobLink := path.Join(f.OwnerDid(), f.RepoName, "blob", ref, treePath)

	s.pages.RepoTree(w, pages.RepoTreeParams{
		LoggedInUser:     user,
		BreadCrumbs:      breadcrumbs,
		BaseTreeLink:     baseTreeLink,
		BaseBlobLink:     baseBlobLink,
		RepoInfo:         f.RepoInfo(s, user),
		RepoTreeResponse: result,
	})
	return
}

func (s *State) RepoTags(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}

	resp, err := http.Get(fmt.Sprintf("%s://%s/%s/%s/tags", protocol, f.Knot, f.OwnerDid(), f.RepoName))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	var result types.RepoTagsResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		return
	}

	user := s.auth.GetUser(r)
	s.pages.RepoTags(w, pages.RepoTagsParams{
		LoggedInUser:     user,
		RepoInfo:         f.RepoInfo(s, user),
		RepoTagsResponse: result,
	})
	return
}

func (s *State) RepoBranches(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	us, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Println("failed to create unsigned client", err)
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

	user := s.auth.GetUser(r)
	s.pages.RepoBranches(w, pages.RepoBranchesParams{
		LoggedInUser:         user,
		RepoInfo:             f.RepoInfo(s, user),
		RepoBranchesResponse: result,
	})
	return
}

func (s *State) RepoBlob(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	filePath := chi.URLParam(r, "*")
	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}
	resp, err := http.Get(fmt.Sprintf("%s://%s/%s/%s/blob/%s/%s", protocol, f.Knot, f.OwnerDid(), f.RepoName, ref, filePath))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	var result types.RepoBlobResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		return
	}

	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.RepoName, fmt.Sprintf("/%s/%s/tree/%s", f.OwnerDid(), f.RepoName, ref)})
	if filePath != "" {
		for idx, elem := range strings.Split(filePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], elem)})
		}
	}

	user := s.auth.GetUser(r)
	s.pages.RepoBlob(w, pages.RepoBlobParams{
		LoggedInUser:     user,
		RepoInfo:         f.RepoInfo(s, user),
		RepoBlobResponse: result,
		BreadCrumbs:      breadcrumbs,
	})
	return
}

func (s *State) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	collaborator := r.FormValue("collaborator")
	if collaborator == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	collaboratorIdent, err := s.resolver.ResolveIdent(r.Context(), collaborator)
	if err != nil {
		w.Write([]byte("failed to resolve collaborator did to a handle"))
		return
	}
	log.Printf("adding %s to %s\n", collaboratorIdent.Handle.String(), f.Knot)

	// TODO: create an atproto record for this

	secret, err := db.GetRegistrationKey(s.db, f.Knot)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", f.Knot, err)
		return
	}

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Println("failed to create client to ", f.Knot)
		return
	}

	ksResp, err := ksClient.AddCollaborator(f.OwnerDid(), f.RepoName, collaboratorIdent.DID.String())
	if err != nil {
		log.Printf("failed to make request to %s: %s", f.Knot, err)
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		w.Write([]byte(fmt.Sprint("knotserver failed to add collaborator: ", err)))
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		w.Write([]byte(fmt.Sprint("failed to add collaborator: ", err)))
		return
	}
	defer func() {
		tx.Rollback()
		err = s.enforcer.E.LoadPolicy()
		if err != nil {
			log.Println("failed to rollback policies")
		}
	}()

	err = s.enforcer.AddCollaborator(collaboratorIdent.DID.String(), f.Knot, f.OwnerSlashRepo())
	if err != nil {
		w.Write([]byte(fmt.Sprint("failed to add collaborator: ", err)))
		return
	}

	err = db.AddCollaborator(s.db, collaboratorIdent.DID.String(), f.OwnerDid(), f.RepoName, f.Knot)
	if err != nil {
		w.Write([]byte(fmt.Sprint("failed to add collaborator: ", err)))
		return
	}

	err = tx.Commit()
	if err != nil {
		log.Println("failed to commit changes", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = s.enforcer.E.SavePolicy()
	if err != nil {
		log.Println("failed to update ACLs", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(fmt.Sprint("added collaborator: ", collaboratorIdent.Handle.String())))

}

func (s *State) RepoSettings(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// for now, this is just pubkeys
		user := s.auth.GetUser(r)
		repoCollaborators, err := f.Collaborators(r.Context(), s)
		if err != nil {
			log.Println("failed to get collaborators", err)
		}

		isCollaboratorInviteAllowed := false
		if user != nil {
			ok, err := s.enforcer.IsCollaboratorInviteAllowed(user.Did, f.Knot, f.OwnerSlashRepo())
			if err == nil && ok {
				isCollaboratorInviteAllowed = true
			}
		}

		s.pages.RepoSettings(w, pages.RepoSettingsParams{
			LoggedInUser:                user,
			RepoInfo:                    f.RepoInfo(s, user),
			Collaborators:               repoCollaborators,
			IsCollaboratorInviteAllowed: isCollaboratorInviteAllowed,
		})
	}
}

type FullyResolvedRepo struct {
	Knot        string
	OwnerId     identity.Identity
	RepoName    string
	RepoAt      syntax.ATURI
	Description string
	AddedAt     string
}

func (f *FullyResolvedRepo) OwnerDid() string {
	return f.OwnerId.DID.String()
}

func (f *FullyResolvedRepo) OwnerHandle() string {
	return f.OwnerId.Handle.String()
}

func (f *FullyResolvedRepo) OwnerSlashRepo() string {
	p, _ := securejoin.SecureJoin(f.OwnerDid(), f.RepoName)
	return p
}

func (f *FullyResolvedRepo) Collaborators(ctx context.Context, s *State) ([]pages.Collaborator, error) {
	repoCollaborators, err := s.enforcer.E.GetImplicitUsersForResourceByDomain(f.OwnerSlashRepo(), f.Knot)
	if err != nil {
		return nil, err
	}

	var collaborators []pages.Collaborator
	for _, item := range repoCollaborators {
		// currently only two roles: owner and member
		var role string
		if item[3] == "repo:owner" {
			role = "owner"
		} else if item[3] == "repo:collaborator" {
			role = "collaborator"
		} else {
			continue
		}

		did := item[0]

		c := pages.Collaborator{
			Did:    did,
			Handle: "",
			Role:   role,
		}
		collaborators = append(collaborators, c)
	}

	// populate all collborators with handles
	identsToResolve := make([]string, len(collaborators))
	for i, collab := range collaborators {
		identsToResolve[i] = collab.Did
	}

	resolvedIdents := s.resolver.ResolveIdents(ctx, identsToResolve)
	for i, resolved := range resolvedIdents {
		if resolved != nil {
			collaborators[i].Handle = resolved.Handle.String()
		}
	}

	return collaborators, nil
}

func (f *FullyResolvedRepo) RepoInfo(s *State, u *auth.User) pages.RepoInfo {
	isStarred := false
	if u != nil {
		isStarred = db.GetStarStatus(s.db, u.Did, syntax.ATURI(f.RepoAt))
	}

	starCount, err := db.GetStarCount(s.db, f.RepoAt)
	if err != nil {
		log.Println("failed to get star count for ", f.RepoAt)
	}
	issueCount, err := db.GetIssueCount(s.db, f.RepoAt)
	if err != nil {
		log.Println("failed to get issue count for ", f.RepoAt)
	}
	pullCount, err := db.GetPullCount(s.db, f.RepoAt)
	if err != nil {
		log.Println("failed to get issue count for ", f.RepoAt)
	}

	knot := f.Knot
	if knot == "knot1.tangled.sh" {
		knot = "tangled.sh"
	}

	return pages.RepoInfo{
		OwnerDid:    f.OwnerDid(),
		OwnerHandle: f.OwnerHandle(),
		Name:        f.RepoName,
		RepoAt:      f.RepoAt,
		Description: f.Description,
		IsStarred:   isStarred,
		Knot:        knot,
		Roles:       RolesInRepo(s, u, f),
		Stats: db.RepoStats{
			StarCount:  starCount,
			IssueCount: issueCount,
			PullCount:  pullCount,
		},
	}
}

func (s *State) RepoSingleIssue(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, comments, err := db.GetIssueWithComments(s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue and comments", err)
		s.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	issueOwnerIdent, err := s.resolver.ResolveIdent(r.Context(), issue.OwnerDid)
	if err != nil {
		log.Println("failed to resolve issue owner", err)
	}

	identsToResolve := make([]string, len(comments))
	for i, comment := range comments {
		identsToResolve[i] = comment.OwnerDid
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

	s.pages.RepoSingleIssue(w, pages.RepoSingleIssueParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(s, user),
		Issue:        *issue,
		Comments:     comments,

		IssueOwnerHandle: issueOwnerIdent.Handle.String(),
		DidHandleMap:     didHandleMap,
	})

}

func (s *State) CloseIssue(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, err := db.GetIssue(s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		s.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
		return
	}

	collaborators, err := f.Collaborators(r.Context(), s)
	if err != nil {
		log.Println("failed to fetch repo collaborators: %w", err)
	}
	isCollaborator := slices.ContainsFunc(collaborators, func(collab pages.Collaborator) bool {
		return user.Did == collab.Did
	})
	isIssueOwner := user.Did == issue.OwnerDid

	// TODO: make this more granular
	if isIssueOwner || isCollaborator {

		closed := tangled.RepoIssueStateClosed

		client, _ := s.auth.AuthorizedClient(r)
		_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueStateNSID,
			Repo:       issue.OwnerDid,
			Rkey:       s.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoIssueState{
					Issue: issue.IssueAt,
					State: &closed,
				},
			},
		})

		if err != nil {
			log.Println("failed to update issue state", err)
			s.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
			return
		}

		err := db.CloseIssue(s.db, f.RepoAt, issueIdInt)
		if err != nil {
			log.Println("failed to close issue", err)
			s.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issueIdInt))
		return
	} else {
		log.Println("user is not permitted to close issue")
		http.Error(w, "for biden", http.StatusUnauthorized)
		return
	}
}

func (s *State) ReopenIssue(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	if user.Did == f.OwnerDid() {
		err := db.ReopenIssue(s.db, f.RepoAt, issueIdInt)
		if err != nil {
			log.Println("failed to reopen issue", err)
			s.pages.Notice(w, "issue-action", "Failed to reopen issue. Try again later.")
			return
		}
		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issueIdInt))
		return
	} else {
		log.Println("user is not the owner of the repo")
		http.Error(w, "forbidden", http.StatusUnauthorized)
		return
	}
}

func (s *State) IssueComment(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	switch r.Method {
	case http.MethodPost:
		body := r.FormValue("body")
		if body == "" {
			s.pages.Notice(w, "issue", "Body is required")
			return
		}

		commentId := rand.IntN(1000000)

		err := db.NewComment(s.db, &db.Comment{
			OwnerDid:  user.Did,
			RepoAt:    f.RepoAt,
			Issue:     issueIdInt,
			CommentId: commentId,
			Body:      body,
		})
		if err != nil {
			log.Println("failed to create comment", err)
			s.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		createdAt := time.Now().Format(time.RFC3339)
		commentIdInt64 := int64(commentId)
		ownerDid := user.Did
		issueAt, err := db.GetIssueAt(s.db, f.RepoAt, issueIdInt)
		if err != nil {
			log.Println("failed to get issue at", err)
			s.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		atUri := f.RepoAt.String()
		client, _ := s.auth.AuthorizedClient(r)
		_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueCommentNSID,
			Repo:       user.Did,
			Rkey:       s.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoIssueComment{
					Repo:      &atUri,
					Issue:     issueAt,
					CommentId: &commentIdInt64,
					Owner:     &ownerDid,
					Body:      &body,
					CreatedAt: &createdAt,
				},
			},
		})
		if err != nil {
			log.Println("failed to create comment", err)
			s.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d#comment-%d", f.OwnerSlashRepo(), issueIdInt, commentId))
		return
	}
}

func (s *State) RepoIssues(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	state := params.Get("state")
	isOpen := true
	switch state {
	case "open":
		isOpen = true
	case "closed":
		isOpen = false
	default:
		isOpen = true
	}

	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issues, err := db.GetIssues(s.db, f.RepoAt, isOpen)
	if err != nil {
		log.Println("failed to get issues", err)
		s.pages.Notice(w, "issues", "Failed to load issues. Try again later.")
		return
	}

	identsToResolve := make([]string, len(issues))
	for i, issue := range issues {
		identsToResolve[i] = issue.OwnerDid
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

	s.pages.RepoIssues(w, pages.RepoIssuesParams{
		LoggedInUser:    s.auth.GetUser(r),
		RepoInfo:        f.RepoInfo(s, user),
		Issues:          issues,
		DidHandleMap:    didHandleMap,
		FilteringByOpen: isOpen,
	})
	return
}

func (s *State) NewIssue(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.pages.RepoNewIssue(w, pages.RepoNewIssueParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(s, user),
		})
	case http.MethodPost:
		title := r.FormValue("title")
		body := r.FormValue("body")

		if title == "" || body == "" {
			s.pages.Notice(w, "issues", "Title and body are required")
			return
		}

		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			s.pages.Notice(w, "issues", "Failed to create issue, try again later")
			return
		}

		err = db.NewIssue(tx, &db.Issue{
			RepoAt:   f.RepoAt,
			Title:    title,
			Body:     body,
			OwnerDid: user.Did,
		})
		if err != nil {
			log.Println("failed to create issue", err)
			s.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		issueId, err := db.GetIssueId(s.db, f.RepoAt)
		if err != nil {
			log.Println("failed to get issue id", err)
			s.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		client, _ := s.auth.AuthorizedClient(r)
		atUri := f.RepoAt.String()
		resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueNSID,
			Repo:       user.Did,
			Rkey:       s.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoIssue{
					Repo:    atUri,
					Title:   title,
					Body:    &body,
					Owner:   user.Did,
					IssueId: int64(issueId),
				},
			},
		})
		if err != nil {
			log.Println("failed to create issue", err)
			s.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		err = db.SetIssueAt(s.db, f.RepoAt, issueId, resp.Uri)
		if err != nil {
			log.Println("failed to set issue at", err)
			s.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issueId))
		return
	}
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

func (s *State) MergePull(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
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

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Printf("failed to create signed client for %s: %s", f.Knot, err)
		s.pages.Notice(w, "pull-merge-error", "Failed to merge pull request. Try again later.")
		return
	}

	// Merge the pull request
	resp, err := ksClient.Merge([]byte(pull.Patch), user.Did, f.RepoName, pull.TargetBranch)
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

func (s *State) PullComment(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	pullId := chi.URLParam(r, "pull")
	pullIdInt, err := strconv.Atoi(pullId)
	if err != nil {
		http.Error(w, "bad pull id", http.StatusBadRequest)
		log.Println("failed to parse pull id", err)
		return
	}

	switch r.Method {
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
		defer tx.Rollback() // Will be ignored if we commit

		commentId := rand.IntN(1000000)
		createdAt := time.Now().Format(time.RFC3339)
		commentIdInt64 := int64(commentId)
		ownerDid := user.Did

		pullAt, err := db.GetPullAt(s.db, f.RepoAt, pullIdInt)
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
					CommentId: &commentIdInt64,
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
		err = db.NewPullComment(tx, &db.PullComment{
			OwnerDid:  user.Did,
			RepoAt:    f.RepoAt.String(),
			CommentId: commentId,
			PullId:    pullIdInt,
			Body:      body,
			CommentAt: atResp.Uri,
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

		s.pages.HxLocation(w, fmt.Sprintf("/%s/pulls/%d#comment-%d", f.OwnerSlashRepo(), pullIdInt, commentId))
		return
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

func fullyResolvedRepo(r *http.Request) (*FullyResolvedRepo, error) {
	repoName := chi.URLParam(r, "repo")
	knot, ok := r.Context().Value("knot").(string)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}
	id, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}

	repoAt, ok := r.Context().Value("repoAt").(string)
	if !ok {
		log.Println("malformed middleware")
		return nil, fmt.Errorf("malformed middleware")
	}

	parsedRepoAt, err := syntax.ParseATURI(repoAt)
	if err != nil {
		log.Println("malformed repo at-uri")
		return nil, fmt.Errorf("malformed middleware")
	}

	// pass through values from the middleware
	description, ok := r.Context().Value("repoDescription").(string)
	addedAt, ok := r.Context().Value("repoAddedAt").(string)

	return &FullyResolvedRepo{
		Knot:        knot,
		OwnerId:     id,
		RepoName:    repoName,
		RepoAt:      parsedRepoAt,
		Description: description,
		AddedAt:     addedAt,
	}, nil
}

func RolesInRepo(s *State, u *auth.User, f *FullyResolvedRepo) pages.RolesInRepo {
	if u != nil {
		r := s.enforcer.GetPermissionsInRepo(u.Did, f.Knot, f.OwnerSlashRepo())
		return pages.RolesInRepo{r}
	} else {
		return pages.RolesInRepo{}
	}
}
