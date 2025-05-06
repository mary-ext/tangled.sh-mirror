package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mathrand "math/rand/v2"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/auth"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/appview/pages/repoinfo"
	"tangled.sh/tangled.sh/core/appview/pagination"
	"tangled.sh/tangled.sh/core/types"

	"github.com/bluesky-social/indigo/atproto/data"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
)

func (s *State) RepoIndex(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoIndex")
	defer span.End()

	ref := chi.URLParam(r, "ref")
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to fully resolve repo")
		return
	}

	us, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", f.Knot)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create unsigned client")
		s.pages.Error503(w)
		return
	}

	resp, err := us.Index(f.OwnerDid(), f.RepoName, ref)
	if err != nil {
		s.pages.Error503(w)
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "error reading response body")
		return
	}

	var result types.RepoIndexResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Printf("Error unmarshalling response body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "error unmarshalling response body")
		return
	}

	tagMap := make(map[string][]string)
	for _, tag := range result.Tags {
		hash := tag.Hash
		if tag.Tag != nil {
			hash = tag.Tag.Target.String()
		}
		tagMap[hash] = append(tagMap[hash], tag.Name)
	}

	for _, branch := range result.Branches {
		hash := branch.Hash
		tagMap[hash] = append(tagMap[hash], branch.Name)
	}

	slices.SortFunc(result.Branches, func(a, b types.Branch) int {
		if a.Name == result.Ref {
			return -1
		}
		if a.IsDefault {
			return -1
		}
		if b.IsDefault {
			return 1
		}
		if a.Commit != nil {
			if a.Commit.Author.When.Before(b.Commit.Author.When) {
				return 1
			} else {
				return -1
			}
		}
		return strings.Compare(a.Name, b.Name) * -1
	})

	commitCount := len(result.Commits)
	branchCount := len(result.Branches)
	tagCount := len(result.Tags)
	fileCount := len(result.Files)

	span.SetAttributes(
		attribute.Int("commits.count", commitCount),
		attribute.Int("branches.count", branchCount),
		attribute.Int("tags.count", tagCount),
		attribute.Int("files.count", fileCount),
	)

	commitCount, branchCount, tagCount = balanceIndexItems(commitCount, branchCount, tagCount, fileCount)
	commitsTrunc := result.Commits[:min(commitCount, len(result.Commits))]
	tagsTrunc := result.Tags[:min(tagCount, len(result.Tags))]
	branchesTrunc := result.Branches[:min(branchCount, len(result.Branches))]

	emails := uniqueEmails(commitsTrunc)

	user := s.auth.GetUser(r)
	s.pages.RepoIndexPage(w, pages.RepoIndexParams{
		LoggedInUser:       user,
		RepoInfo:           f.RepoInfo(ctx, s, user),
		TagMap:             tagMap,
		RepoIndexResponse:  result,
		CommitsTrunc:       commitsTrunc,
		TagsTrunc:          tagsTrunc,
		BranchesTrunc:      branchesTrunc,
		EmailToDidOrHandle: EmailToDidOrHandle(s, emails),
	})
	return
}

func (s *State) RepoLog(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoLog")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to fully resolve repo")
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
	span.SetAttributes(attribute.Int("page", page), attribute.String("ref", ref))

	us, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Println("failed to create unsigned client", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create unsigned client")
		return
	}

	resp, err := us.Log(f.OwnerDid(), f.RepoName, ref, page)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "error reading response body")
		return
	}

	var repolog types.RepoLogResponse
	err = json.Unmarshal(body, &repolog)
	if err != nil {
		log.Println("failed to parse json response", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse json response")
		return
	}

	span.SetAttributes(attribute.Int("commits.count", len(repolog.Commits)))

	result, err := us.Tags(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver for tags")
		return
	}

	tagMap := make(map[string][]string)
	for _, tag := range result.Tags {
		hash := tag.Hash
		if tag.Tag != nil {
			hash = tag.Tag.Target.String()
		}
		tagMap[hash] = append(tagMap[hash], tag.Name)
	}

	span.SetAttributes(attribute.Int("tags.count", len(result.Tags)))

	user := s.auth.GetUser(r)
	s.pages.RepoLog(w, pages.RepoLogParams{
		LoggedInUser:       user,
		TagMap:             tagMap,
		RepoInfo:           f.RepoInfo(ctx, s, user),
		RepoLogResponse:    repolog,
		EmailToDidOrHandle: EmailToDidOrHandle(s, uniqueEmails(repolog.Commits)),
	})
	return
}

func (s *State) RepoDescriptionEdit(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoDescriptionEdit")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	user := s.auth.GetUser(r)
	s.pages.EditRepoDescriptionFragment(w, pages.RepoDescriptionParams{
		RepoInfo: f.RepoInfo(ctx, s, user),
	})
	return
}

func (s *State) RepoDescription(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoDescription")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	repoAt := f.RepoAt
	rkey := repoAt.RecordKey().String()
	if rkey == "" {
		log.Println("invalid aturi for repo", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid aturi for repo")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	user := s.auth.GetUser(r)
	span.SetAttributes(attribute.String("method", r.Method))

	switch r.Method {
	case http.MethodGet:
		s.pages.RepoDescriptionFragment(w, pages.RepoDescriptionParams{
			RepoInfo: f.RepoInfo(ctx, s, user),
		})
		return
	case http.MethodPut:
		user := s.auth.GetUser(r)
		newDescription := r.FormValue("description")
		span.SetAttributes(attribute.String("description", newDescription))
		client, _ := s.auth.AuthorizedClient(r)

		// optimistic update
		err = db.UpdateDescription(ctx, s.db, string(repoAt), newDescription)
		if err != nil {
			log.Println("failed to perform update-description query", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to update description in database")
			s.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// this is a bit of a pain because the golang atproto impl does not allow nil SwapRecord field
		//
		// SwapRecord is optional and should happen automagically, but given that it does not, we have to perform two requests
		ex, err := comatproto.RepoGetRecord(ctx, client, "", tangled.RepoNSID, user.Did, rkey)
		if err != nil {
			// failed to get record
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to get record from PDS")
			s.pages.Notice(w, "repo-notice", "Failed to update description, no record found on PDS.")
			return
		}

		_, err = comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			SwapRecord: ex.Cid,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.Repo{
					Knot:        f.Knot,
					Name:        f.RepoName,
					Owner:       user.Did,
					CreatedAt:   f.CreatedAt,
					Description: &newDescription,
				},
			},
		})

		if err != nil {
			log.Println("failed to perform update-description query", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to put record to PDS")
			// failed to get record
			s.pages.Notice(w, "repo-notice", "Failed to update description, unable to save to PDS.")
			return
		}

		newRepoInfo := f.RepoInfo(ctx, s, user)
		newRepoInfo.Description = newDescription

		s.pages.RepoDescriptionFragment(w, pages.RepoDescriptionParams{
			RepoInfo: newRepoInfo,
		})
		return
	}
}

func (s *State) RepoCommit(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoCommit")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to fully resolve repo")
		return
	}
	ref := chi.URLParam(r, "ref")
	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}

	span.SetAttributes(attribute.String("ref", ref), attribute.String("protocol", protocol))

	if !plumbing.IsHash(ref) {
		span.SetAttributes(attribute.Bool("invalid_hash", true))
		s.pages.Error404(w)
		return
	}

	requestURL := fmt.Sprintf("%s://%s/%s/%s/commit/%s", protocol, f.Knot, f.OwnerDid(), f.RepoName, ref)
	span.SetAttributes(attribute.String("request_url", requestURL))

	resp, err := http.Get(requestURL)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "error reading response body")
		return
	}

	var result types.RepoCommitResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse response")
		return
	}

	user := s.auth.GetUser(r)
	s.pages.RepoCommit(w, pages.RepoCommitParams{
		LoggedInUser:       user,
		RepoInfo:           f.RepoInfo(ctx, s, user),
		RepoCommitResponse: result,
		EmailToDidOrHandle: EmailToDidOrHandle(s, []string{result.Diff.Commit.Author.Email}),
	})
	return
}

func (s *State) RepoTree(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoTree")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to fully resolve repo")
		return
	}

	ref := chi.URLParam(r, "ref")
	treePath := chi.URLParam(r, "*")
	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}

	span.SetAttributes(
		attribute.String("ref", ref),
		attribute.String("tree_path", treePath),
		attribute.String("protocol", protocol),
	)

	requestURL := fmt.Sprintf("%s://%s/%s/%s/tree/%s/%s", protocol, f.Knot, f.OwnerDid(), f.RepoName, ref, treePath)
	span.SetAttributes(attribute.String("request_url", requestURL))

	resp, err := http.Get(requestURL)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "error reading response body")
		return
	}

	var result types.RepoTreeResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse response")
		return
	}

	// redirects tree paths trying to access a blob; in this case the result.Files is unpopulated,
	// so we can safely redirect to the "parent" (which is the same file).
	if len(result.Files) == 0 && result.Parent == treePath {
		redirectURL := fmt.Sprintf("/%s/blob/%s/%s", f.OwnerSlashRepo(), ref, result.Parent)
		span.SetAttributes(attribute.String("redirect_url", redirectURL))
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	user := s.auth.GetUser(r)

	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.RepoName, fmt.Sprintf("/%s/tree/%s", f.OwnerSlashRepo(), ref)})
	if treePath != "" {
		for idx, elem := range strings.Split(treePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], elem)})
		}
	}

	baseTreeLink := path.Join(f.OwnerSlashRepo(), "tree", ref, treePath)
	baseBlobLink := path.Join(f.OwnerSlashRepo(), "blob", ref, treePath)

	s.pages.RepoTree(w, pages.RepoTreeParams{
		LoggedInUser:     user,
		BreadCrumbs:      breadcrumbs,
		BaseTreeLink:     baseTreeLink,
		BaseBlobLink:     baseBlobLink,
		RepoInfo:         f.RepoInfo(ctx, s, user),
		RepoTreeResponse: result,
	})
	return
}

func (s *State) RepoTags(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoTags")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get repo and knot")
		return
	}

	us, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Println("failed to create unsigned client", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create unsigned client")
		return
	}

	result, err := us.Tags(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver")
		return
	}

	span.SetAttributes(attribute.Int("tags.count", len(result.Tags)))

	artifacts, err := db.GetArtifact(s.db, db.Filter("repo_at", f.RepoAt))
	if err != nil {
		log.Println("failed grab artifacts", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to grab artifacts")
		return
	}

	span.SetAttributes(attribute.Int("artifacts.count", len(artifacts)))

	// convert artifacts to map for easy UI building
	artifactMap := make(map[plumbing.Hash][]db.Artifact)
	for _, a := range artifacts {
		artifactMap[a.Tag] = append(artifactMap[a.Tag], a)
	}

	var danglingArtifacts []db.Artifact
	for _, a := range artifacts {
		found := false
		for _, t := range result.Tags {
			if t.Tag != nil {
				if t.Tag.Hash == a.Tag {
					found = true
				}
			}
		}

		if !found {
			danglingArtifacts = append(danglingArtifacts, a)
		}
	}

	span.SetAttributes(attribute.Int("dangling_artifacts.count", len(danglingArtifacts)))

	user := s.auth.GetUser(r)
	s.pages.RepoTags(w, pages.RepoTagsParams{
		LoggedInUser:      user,
		RepoInfo:          f.RepoInfo(ctx, s, user),
		RepoTagsResponse:  *result,
		ArtifactMap:       artifactMap,
		DanglingArtifacts: danglingArtifacts,
	})
	return
}

func (s *State) RepoBranches(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoBranches")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get repo and knot")
		return
	}

	us, err := NewUnsignedClient(f.Knot, s.config.Dev)
	if err != nil {
		log.Println("failed to create unsigned client", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create unsigned client")
		return
	}

	resp, err := us.Branches(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "error reading response body")
		return
	}

	var result types.RepoBranchesResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse response")
		return
	}

	span.SetAttributes(attribute.Int("branches.count", len(result.Branches)))

	slices.SortFunc(result.Branches, func(a, b types.Branch) int {
		if a.IsDefault {
			return -1
		}
		if b.IsDefault {
			return 1
		}
		if a.Commit != nil {
			if a.Commit.Author.When.Before(b.Commit.Author.When) {
				return 1
			} else {
				return -1
			}
		}
		return strings.Compare(a.Name, b.Name) * -1
	})

	user := s.auth.GetUser(r)
	s.pages.RepoBranches(w, pages.RepoBranchesParams{
		LoggedInUser:         user,
		RepoInfo:             f.RepoInfo(ctx, s, user),
		RepoBranchesResponse: result,
	})
	return
}

func (s *State) RepoBlob(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoBlob")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get repo and knot")
		return
	}

	ref := chi.URLParam(r, "ref")
	filePath := chi.URLParam(r, "*")
	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}

	span.SetAttributes(
		attribute.String("ref", ref),
		attribute.String("file_path", filePath),
		attribute.String("protocol", protocol),
	)

	requestURL := fmt.Sprintf("%s://%s/%s/%s/blob/%s/%s", protocol, f.Knot, f.OwnerDid(), f.RepoName, ref, filePath)
	span.SetAttributes(attribute.String("request_url", requestURL))

	resp, err := http.Get(requestURL)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "error reading response body")
		return
	}

	var result types.RepoBlobResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse response")
		return
	}

	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.RepoName, fmt.Sprintf("/%s/tree/%s", f.OwnerSlashRepo(), ref)})
	if filePath != "" {
		for idx, elem := range strings.Split(filePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], elem)})
		}
	}

	showRendered := false
	renderToggle := false

	if markup.GetFormat(result.Path) == markup.FormatMarkdown {
		renderToggle = true
		showRendered = r.URL.Query().Get("code") != "true"
	}

	span.SetAttributes(
		attribute.Bool("is_binary", result.IsBinary),
		attribute.Bool("show_rendered", showRendered),
		attribute.Bool("render_toggle", renderToggle),
	)

	user := s.auth.GetUser(r)
	s.pages.RepoBlob(w, pages.RepoBlobParams{
		LoggedInUser:     user,
		RepoInfo:         f.RepoInfo(ctx, s, user),
		RepoBlobResponse: result,
		BreadCrumbs:      breadcrumbs,
		ShowRendered:     showRendered,
		RenderToggle:     renderToggle,
	})
	return
}

func (s *State) RepoBlobRaw(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoBlobRaw")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get repo and knot")
		return
	}

	ref := chi.URLParam(r, "ref")
	filePath := chi.URLParam(r, "*")

	protocol := "http"
	if !s.config.Dev {
		protocol = "https"
	}

	span.SetAttributes(
		attribute.String("ref", ref),
		attribute.String("file_path", filePath),
		attribute.String("protocol", protocol),
	)

	requestURL := fmt.Sprintf("%s://%s/%s/%s/blob/%s/%s", protocol, f.Knot, f.OwnerDid(), f.RepoName, ref, filePath)
	span.SetAttributes(attribute.String("request_url", requestURL))

	resp, err := http.Get(requestURL)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to reach knotserver")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "error reading response body")
		return
	}

	var result types.RepoBlobResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse response")
		return
	}

	span.SetAttributes(attribute.Bool("is_binary", result.IsBinary))

	if result.IsBinary {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(result.Contents))
	return
}

func (s *State) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "AddCollaborator")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get repo and knot")
		return
	}

	collaborator := r.FormValue("collaborator")
	if collaborator == "" {
		span.SetAttributes(attribute.String("error", "malformed_form"))
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	span.SetAttributes(attribute.String("collaborator", collaborator))

	collaboratorIdent, err := s.resolver.ResolveIdent(ctx, collaborator)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve collaborator")
		w.Write([]byte("failed to resolve collaborator did to a handle"))
		return
	}
	log.Printf("adding %s to %s\n", collaboratorIdent.Handle.String(), f.Knot)
	span.SetAttributes(
		attribute.String("collaborator_did", collaboratorIdent.DID.String()),
		attribute.String("collaborator_handle", collaboratorIdent.Handle.String()),
	)

	// TODO: create an atproto record for this

	secret, err := db.GetRegistrationKey(s.db, f.Knot)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", f.Knot, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "no key found for domain")
		return
	}

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Println("failed to create client to ", f.Knot)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create signed client")
		return
	}

	ksResp, err := ksClient.AddCollaborator(f.OwnerDid(), f.RepoName, collaboratorIdent.DID.String())
	if err != nil {
		log.Printf("failed to make request to %s: %s", f.Knot, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to make request to knotserver")
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		span.SetAttributes(attribute.Int("status_code", ksResp.StatusCode))
		w.Write([]byte(fmt.Sprint("knotserver failed to add collaborator: ", err)))
		return
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("failed to start tx")
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to start transaction")
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

	err = s.enforcer.AddCollaborator(collaboratorIdent.DID.String(), f.Knot, f.DidSlashRepo())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to add collaborator to enforcer")
		w.Write([]byte(fmt.Sprint("failed to add collaborator: ", err)))
		return
	}

	err = db.AddCollaborator(ctx, s.db, collaboratorIdent.DID.String(), f.OwnerDid(), f.RepoName, f.Knot)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to add collaborator to database")
		w.Write([]byte(fmt.Sprint("failed to add collaborator: ", err)))
		return
	}

	err = tx.Commit()
	if err != nil {
		log.Println("failed to commit changes", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to commit transaction")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = s.enforcer.E.SavePolicy()
	if err != nil {
		log.Println("failed to update ACLs", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to save enforcer policy")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(fmt.Sprint("added collaborator: ", collaboratorIdent.Handle.String())))
}

func (s *State) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "DeleteRepo")
	defer span.End()

	user := s.auth.GetUser(r)

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get repo and knot")
		return
	}

	span.SetAttributes(
		attribute.String("repo_name", f.RepoName),
		attribute.String("knot", f.Knot),
		attribute.String("owner_did", f.OwnerDid()),
	)

	// remove record from pds
	xrpcClient, _ := s.auth.AuthorizedClient(r)
	repoRkey := f.RepoAt.RecordKey().String()
	_, err = comatproto.RepoDeleteRecord(ctx, xrpcClient, &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       user.Did,
		Rkey:       repoRkey,
	})
	if err != nil {
		log.Printf("failed to delete record: %s", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to delete record from PDS")
		s.pages.Notice(w, "settings-delete", "Failed to delete repository from PDS.")
		return
	}
	log.Println("removed repo record ", f.RepoAt.String())
	span.SetAttributes(attribute.String("repo_at", f.RepoAt.String()))

	secret, err := db.GetRegistrationKey(s.db, f.Knot)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", f.Knot, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "no key found for domain")
		return
	}

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Println("failed to create client to ", f.Knot)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create client")
		return
	}

	ksResp, err := ksClient.RemoveRepo(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Printf("failed to make request to %s: %s", f.Knot, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to make request to knotserver")
		return
	}

	span.SetAttributes(attribute.Int("ks_status_code", ksResp.StatusCode))
	if ksResp.StatusCode != http.StatusNoContent {
		log.Println("failed to remove repo from knot, continuing anyway ", f.Knot)
		span.SetAttributes(attribute.Bool("knot_remove_failed", true))
	} else {
		log.Println("removed repo from knot ", f.Knot)
		span.SetAttributes(attribute.Bool("knot_remove_success", true))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Println("failed to start tx")
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to start transaction")
		w.Write([]byte(fmt.Sprint("failed to add collaborator: ", err)))
		return
	}
	defer func() {
		tx.Rollback()
		err = s.enforcer.E.LoadPolicy()
		if err != nil {
			log.Println("failed to rollback policies")
			span.RecordError(err)
		}
	}()

	// remove collaborator RBAC
	repoCollaborators, err := s.enforcer.E.GetImplicitUsersForResourceByDomain(f.DidSlashRepo(), f.Knot)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get collaborators")
		s.pages.Notice(w, "settings-delete", "Failed to remove collaborators")
		return
	}
	span.SetAttributes(attribute.Int("collaborators.count", len(repoCollaborators)))

	for _, c := range repoCollaborators {
		did := c[0]
		s.enforcer.RemoveCollaborator(did, f.Knot, f.DidSlashRepo())
	}
	log.Println("removed collaborators")

	// remove repo RBAC
	err = s.enforcer.RemoveRepo(f.OwnerDid(), f.Knot, f.DidSlashRepo())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to remove repo RBAC")
		s.pages.Notice(w, "settings-delete", "Failed to update RBAC rules")
		return
	}

	// remove repo from db
	err = db.RemoveRepo(ctx, tx, f.OwnerDid(), f.RepoName)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to remove repo from db")
		s.pages.Notice(w, "settings-delete", "Failed to update appview")
		return
	}
	log.Println("removed repo from db")

	err = tx.Commit()
	if err != nil {
		log.Println("failed to commit changes", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to commit transaction")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = s.enforcer.E.SavePolicy()
	if err != nil {
		log.Println("failed to update ACLs", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to save policy")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.pages.HxRedirect(w, fmt.Sprintf("/%s", f.OwnerDid()))
}

func (s *State) SetDefaultBranch(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "SetDefaultBranch")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get repo and knot")
		return
	}

	branch := r.FormValue("branch")
	if branch == "" {
		span.SetAttributes(attribute.Bool("malformed_form", true))
		span.SetStatus(codes.Error, "malformed form")
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	span.SetAttributes(
		attribute.String("branch", branch),
		attribute.String("repo_name", f.RepoName),
		attribute.String("knot", f.Knot),
		attribute.String("owner_did", f.OwnerDid()),
	)

	secret, err := db.GetRegistrationKey(s.db, f.Knot)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", f.Knot, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "no key found for domain")
		return
	}

	ksClient, err := NewSignedClient(f.Knot, secret, s.config.Dev)
	if err != nil {
		log.Println("failed to create client to ", f.Knot)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create client")
		return
	}

	ksResp, err := ksClient.SetDefaultBranch(f.OwnerDid(), f.RepoName, branch)
	if err != nil {
		log.Printf("failed to make request to %s: %s", f.Knot, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to make request to knotserver")
		return
	}

	span.SetAttributes(attribute.Int("ks_status_code", ksResp.StatusCode))
	if ksResp.StatusCode != http.StatusNoContent {
		span.SetStatus(codes.Error, "failed to set default branch")
		s.pages.Notice(w, "repo-settings", "Failed to set default branch. Try again later.")
		return
	}

	w.Write([]byte(fmt.Sprint("default branch set to: ", branch)))
}

func (s *State) RepoSettings(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoSettings")
	defer span.End()

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get repo and knot")
		return
	}

	span.SetAttributes(
		attribute.String("repo_name", f.RepoName),
		attribute.String("knot", f.Knot),
		attribute.String("owner_did", f.OwnerDid()),
		attribute.String("method", r.Method),
	)

	switch r.Method {
	case http.MethodGet:
		// for now, this is just pubkeys
		user := s.auth.GetUser(r)
		repoCollaborators, err := f.Collaborators(ctx, s)
		if err != nil {
			log.Println("failed to get collaborators", err)
			span.RecordError(err)
			span.SetAttributes(attribute.String("error", "failed_to_get_collaborators"))
		}
		span.SetAttributes(attribute.Int("collaborators.count", len(repoCollaborators)))

		isCollaboratorInviteAllowed := false
		if user != nil {
			ok, err := s.enforcer.IsCollaboratorInviteAllowed(user.Did, f.Knot, f.DidSlashRepo())
			if err == nil && ok {
				isCollaboratorInviteAllowed = true
			}
		}
		span.SetAttributes(attribute.Bool("invite_allowed", isCollaboratorInviteAllowed))

		var branchNames []string
		var defaultBranch string
		us, err := NewUnsignedClient(f.Knot, s.config.Dev)
		if err != nil {
			log.Println("failed to create unsigned client", err)
			span.RecordError(err)
			span.SetAttributes(attribute.String("error", "failed_to_create_unsigned_client"))
		} else {
			resp, err := us.Branches(f.OwnerDid(), f.RepoName)
			if err != nil {
				log.Println("failed to reach knotserver", err)
				span.RecordError(err)
				span.SetAttributes(attribute.String("error", "failed_to_reach_knotserver_for_branches"))
			} else {
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Printf("Error reading response body: %v", err)
					span.RecordError(err)
					span.SetAttributes(attribute.String("error", "failed_to_read_branches_response"))
				} else {
					var result types.RepoBranchesResponse
					err = json.Unmarshal(body, &result)
					if err != nil {
						log.Println("failed to parse response:", err)
						span.RecordError(err)
						span.SetAttributes(attribute.String("error", "failed_to_parse_branches_response"))
					} else {
						for _, branch := range result.Branches {
							branchNames = append(branchNames, branch.Name)
						}
						span.SetAttributes(attribute.Int("branches.count", len(branchNames)))
					}
				}
			}

			defaultBranchResp, err := us.DefaultBranch(f.OwnerDid(), f.RepoName)
			if err != nil {
				log.Println("failed to reach knotserver", err)
				span.RecordError(err)
				span.SetAttributes(attribute.String("error", "failed_to_reach_knotserver_for_default_branch"))
			} else {
				defaultBranch = defaultBranchResp.Branch
				span.SetAttributes(attribute.String("default_branch", defaultBranch))
			}
		}
		s.pages.RepoSettings(w, pages.RepoSettingsParams{
			LoggedInUser:                user,
			RepoInfo:                    f.RepoInfo(ctx, s, user),
			Collaborators:               repoCollaborators,
			IsCollaboratorInviteAllowed: isCollaboratorInviteAllowed,
			Branches:                    branchNames,
			DefaultBranch:               defaultBranch,
		})
	}
}

type FullyResolvedRepo struct {
	Knot        string
	OwnerId     identity.Identity
	RepoName    string
	RepoAt      syntax.ATURI
	Description string
	CreatedAt   string
	Ref         string
}

func (f *FullyResolvedRepo) OwnerDid() string {
	return f.OwnerId.DID.String()
}

func (f *FullyResolvedRepo) OwnerHandle() string {
	return f.OwnerId.Handle.String()
}

func (f *FullyResolvedRepo) OwnerSlashRepo() string {
	handle := f.OwnerId.Handle

	var p string
	if handle != "" && !handle.IsInvalidHandle() {
		p, _ = securejoin.SecureJoin(fmt.Sprintf("@%s", handle), f.RepoName)
	} else {
		p, _ = securejoin.SecureJoin(f.OwnerDid(), f.RepoName)
	}

	return p
}

func (f *FullyResolvedRepo) DidSlashRepo() string {
	p, _ := securejoin.SecureJoin(f.OwnerDid(), f.RepoName)
	return p
}

func (f *FullyResolvedRepo) Collaborators(ctx context.Context, s *State) ([]pages.Collaborator, error) {
	repoCollaborators, err := s.enforcer.E.GetImplicitUsersForResourceByDomain(f.DidSlashRepo(), f.Knot)
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

func (f *FullyResolvedRepo) RepoInfo(ctx context.Context, s *State, u *auth.User) repoinfo.RepoInfo {
	ctx, span := s.t.TraceStart(ctx, "RepoInfo")
	defer span.End()

	isStarred := false
	if u != nil {
		isStarred = db.GetStarStatus(s.db, u.Did, syntax.ATURI(f.RepoAt))
		span.SetAttributes(attribute.Bool("is_starred", isStarred))
	}

	starCount, err := db.GetStarCount(s.db, f.RepoAt)
	if err != nil {
		log.Println("failed to get star count for ", f.RepoAt)
		span.RecordError(err)
	}

	issueCount, err := db.GetIssueCount(s.db, f.RepoAt)
	if err != nil {
		log.Println("failed to get issue count for ", f.RepoAt)
		span.RecordError(err)
	}

	pullCount, err := db.GetPullCount(s.db, f.RepoAt)
	if err != nil {
		log.Println("failed to get issue count for ", f.RepoAt)
		span.RecordError(err)
	}

	span.SetAttributes(
		attribute.Int("stats.stars", starCount),
		attribute.Int("stats.issues.open", issueCount.Open),
		attribute.Int("stats.issues.closed", issueCount.Closed),
		attribute.Int("stats.pulls.open", pullCount.Open),
		attribute.Int("stats.pulls.closed", pullCount.Closed),
		attribute.Int("stats.pulls.merged", pullCount.Merged),
	)

	source, err := db.GetRepoSource(ctx, s.db, f.RepoAt)
	if errors.Is(err, sql.ErrNoRows) {
		source = ""
	} else if err != nil {
		log.Println("failed to get repo source for ", f.RepoAt, err)
		span.RecordError(err)
	}

	var sourceRepo *db.Repo
	if source != "" {
		span.SetAttributes(attribute.String("source", source))
		sourceRepo, err = db.GetRepoByAtUri(ctx, s.db, source)
		if err != nil {
			log.Println("failed to get repo by at uri", err)
			span.RecordError(err)
		}
	}

	var sourceHandle *identity.Identity
	if sourceRepo != nil {
		sourceHandle, err = s.resolver.ResolveIdent(ctx, sourceRepo.Did)
		if err != nil {
			log.Println("failed to resolve source repo", err)
			span.RecordError(err)
		} else if sourceHandle != nil {
			span.SetAttributes(attribute.String("source_handle", sourceHandle.Handle.String()))
		}
	}

	knot := f.Knot
	span.SetAttributes(attribute.String("knot", knot))

	var disableFork bool
	us, err := NewUnsignedClient(knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s: %v", knot, err)
		span.RecordError(err)
	} else {
		resp, err := us.Branches(f.OwnerDid(), f.RepoName)
		if err != nil {
			log.Printf("failed to get branches for %s/%s: %v", f.OwnerDid(), f.RepoName, err)
			span.RecordError(err)
		} else {
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("error reading branch response body: %v", err)
				span.RecordError(err)
			} else {
				var branchesResp types.RepoBranchesResponse
				if err := json.Unmarshal(body, &branchesResp); err != nil {
					log.Printf("error parsing branch response: %v", err)
					span.RecordError(err)
				} else {
					disableFork = false
				}

				if len(branchesResp.Branches) == 0 {
					disableFork = true
				}
				span.SetAttributes(
					attribute.Int("branches.count", len(branchesResp.Branches)),
					attribute.Bool("disable_fork", disableFork),
				)
			}
		}
	}

	repoInfo := repoinfo.RepoInfo{
		OwnerDid:    f.OwnerDid(),
		OwnerHandle: f.OwnerHandle(),
		Name:        f.RepoName,
		RepoAt:      f.RepoAt,
		Description: f.Description,
		Ref:         f.Ref,
		IsStarred:   isStarred,
		Knot:        knot,
		Roles:       RolesInRepo(s, u, f),
		Stats: db.RepoStats{
			StarCount:  starCount,
			IssueCount: issueCount,
			PullCount:  pullCount,
		},
		DisableFork: disableFork,
	}

	if sourceRepo != nil {
		repoInfo.Source = sourceRepo
		repoInfo.SourceHandle = sourceHandle.Handle.String()
	}

	return repoInfo
}

func (s *State) RepoSingleIssue(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoSingleIssue")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse issue id")
		return
	}

	span.SetAttributes(attribute.Int("issue_id", issueIdInt))

	issue, comments, err := db.GetIssueWithComments(ctx, s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue and comments", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get issue and comments")
		s.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	span.SetAttributes(
		attribute.Int("comments.count", len(comments)),
		attribute.String("issue.title", issue.Title),
		attribute.String("issue.owner_did", issue.OwnerDid),
	)

	issueOwnerIdent, err := s.resolver.ResolveIdent(ctx, issue.OwnerDid)
	if err != nil {
		log.Println("failed to resolve issue owner", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve issue owner")
	}

	identsToResolve := make([]string, len(comments))
	for i, comment := range comments {
		identsToResolve[i] = comment.OwnerDid
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

	s.pages.RepoSingleIssue(w, pages.RepoSingleIssueParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(ctx, s, user),
		Issue:        *issue,
		Comments:     comments,

		IssueOwnerHandle: issueOwnerIdent.Handle.String(),
		DidHandleMap:     didHandleMap,
	})
}

func (s *State) CloseIssue(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "CloseIssue")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse issue id")
		return
	}

	span.SetAttributes(attribute.Int("issue_id", issueIdInt))

	issue, err := db.GetIssue(ctx, s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get issue")
		s.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
		return
	}

	collaborators, err := f.Collaborators(ctx, s)
	if err != nil {
		log.Println("failed to fetch repo collaborators: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to fetch repo collaborators")
	}
	isCollaborator := slices.ContainsFunc(collaborators, func(collab pages.Collaborator) bool {
		return user.Did == collab.Did
	})
	isIssueOwner := user.Did == issue.OwnerDid

	span.SetAttributes(
		attribute.Bool("is_collaborator", isCollaborator),
		attribute.Bool("is_issue_owner", isIssueOwner),
	)

	// TODO: make this more granular
	if isIssueOwner || isCollaborator {
		closed := tangled.RepoIssueStateClosed

		client, _ := s.auth.AuthorizedClient(r)
		_, err = comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueStateNSID,
			Repo:       user.Did,
			Rkey:       appview.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoIssueState{
					Issue: issue.IssueAt,
					State: closed,
				},
			},
		})

		if err != nil {
			log.Println("failed to update issue state", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to update issue state in PDS")
			s.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
			return
		}

		err := db.CloseIssue(s.db, f.RepoAt, issueIdInt)
		if err != nil {
			log.Println("failed to close issue", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to close issue in database")
			s.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issueIdInt))
		return
	} else {
		log.Println("user is not permitted to close issue")
		span.SetAttributes(attribute.Bool("permission_denied", true))
		http.Error(w, "for biden", http.StatusUnauthorized)
		return
	}
}

func (s *State) ReopenIssue(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "ReopenIssue")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse issue id")
		return
	}

	span.SetAttributes(attribute.Int("issue_id", issueIdInt))

	issue, err := db.GetIssue(ctx, s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get issue")
		s.pages.Notice(w, "issue-action", "Failed to close issue. Try again later.")
		return
	}

	collaborators, err := f.Collaborators(ctx, s)
	if err != nil {
		log.Println("failed to fetch repo collaborators: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to fetch repo collaborators")
	}
	isCollaborator := slices.ContainsFunc(collaborators, func(collab pages.Collaborator) bool {
		return user.Did == collab.Did
	})
	isIssueOwner := user.Did == issue.OwnerDid

	span.SetAttributes(
		attribute.Bool("is_collaborator", isCollaborator),
		attribute.Bool("is_issue_owner", isIssueOwner),
	)

	if isCollaborator || isIssueOwner {
		err := db.ReopenIssue(s.db, f.RepoAt, issueIdInt)
		if err != nil {
			log.Println("failed to reopen issue", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to reopen issue")
			s.pages.Notice(w, "issue-action", "Failed to reopen issue. Try again later.")
			return
		}
		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issueIdInt))
		return
	} else {
		log.Println("user is not the owner of the repo")
		span.SetAttributes(attribute.Bool("permission_denied", true))
		http.Error(w, "forbidden", http.StatusUnauthorized)
		return
	}
}

func (s *State) NewIssueComment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "NewIssueComment")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse issue id")
		return
	}

	span.SetAttributes(
		attribute.Int("issue_id", issueIdInt),
		attribute.String("method", r.Method),
	)

	switch r.Method {
	case http.MethodPost:
		body := r.FormValue("body")
		if body == "" {
			span.SetAttributes(attribute.Bool("missing_body", true))
			s.pages.Notice(w, "issue", "Body is required")
			return
		}

		commentId := mathrand.IntN(1000000)
		rkey := appview.TID()

		span.SetAttributes(
			attribute.Int("comment_id", commentId),
			attribute.String("rkey", rkey),
		)

		err := db.NewIssueComment(s.db, &db.Comment{
			OwnerDid:  user.Did,
			RepoAt:    f.RepoAt,
			Issue:     issueIdInt,
			CommentId: commentId,
			Body:      body,
			Rkey:      rkey,
		})
		if err != nil {
			log.Println("failed to create comment", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create comment in database")
			s.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		createdAt := time.Now().Format(time.RFC3339)
		commentIdInt64 := int64(commentId)
		ownerDid := user.Did
		issueAt, err := db.GetIssueAt(s.db, f.RepoAt, issueIdInt)
		if err != nil {
			log.Println("failed to get issue at", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to get issue at")
			s.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		span.SetAttributes(attribute.String("issue_at", issueAt))

		atUri := f.RepoAt.String()
		client, _ := s.auth.AuthorizedClient(r)
		_, err = comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueCommentNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.RepoIssueComment{
					Repo:      &atUri,
					Issue:     issueAt,
					CommentId: &commentIdInt64,
					Owner:     &ownerDid,
					Body:      body,
					CreatedAt: createdAt,
				},
			},
		})
		if err != nil {
			log.Println("failed to create comment", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create comment in PDS")
			s.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d#comment-%d", f.OwnerSlashRepo(), issueIdInt, commentId))
		return
	}
}

func (s *State) IssueComment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "IssueComment")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse issue id")
		return
	}

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse comment id")
		return
	}

	span.SetAttributes(
		attribute.Int("issue_id", issueIdInt),
		attribute.Int("comment_id", commentIdInt),
	)

	issue, err := db.GetIssue(ctx, s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get issue")
		s.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	comment, err := db.GetComment(s.db, f.RepoAt, issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get comment")
		return
	}

	identity, err := s.resolver.ResolveIdent(ctx, comment.OwnerDid)
	if err != nil {
		log.Println("failed to resolve did")
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve did")
		return
	}

	didHandleMap := make(map[string]string)
	if !identity.Handle.IsInvalidHandle() {
		didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
	} else {
		didHandleMap[identity.DID.String()] = identity.DID.String()
	}

	s.pages.SingleIssueCommentFragment(w, pages.SingleIssueCommentParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(ctx, s, user),
		DidHandleMap: didHandleMap,
		Issue:        issue,
		Comment:      comment,
	})
}

func (s *State) EditIssueComment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "EditIssueComment")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse issue id")
		return
	}

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse comment id")
		return
	}

	span.SetAttributes(
		attribute.Int("issue_id", issueIdInt),
		attribute.Int("comment_id", commentIdInt),
		attribute.String("method", r.Method),
	)

	issue, err := db.GetIssue(ctx, s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get issue")
		s.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	comment, err := db.GetComment(s.db, f.RepoAt, issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get comment")
		return
	}

	if comment.OwnerDid != user.Did {
		http.Error(w, "you are not the author of this comment", http.StatusUnauthorized)
		span.SetAttributes(attribute.Bool("permission_denied", true))
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.pages.EditIssueCommentFragment(w, pages.EditIssueCommentParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(ctx, s, user),
			Issue:        issue,
			Comment:      comment,
		})
	case http.MethodPost:
		// extract form value
		newBody := r.FormValue("body")
		client, _ := s.auth.AuthorizedClient(r)
		rkey := comment.Rkey

		span.SetAttributes(
			attribute.String("new_body", newBody),
			attribute.String("rkey", rkey),
		)

		// optimistic update
		edited := time.Now()
		err = db.EditComment(s.db, comment.RepoAt, comment.Issue, comment.CommentId, newBody)
		if err != nil {
			log.Println("failed to perferom update-description query", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to edit comment in database")
			s.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// rkey is optional, it was introduced later
		if comment.Rkey != "" {
			// update the record on pds
			ex, err := comatproto.RepoGetRecord(ctx, client, "", tangled.RepoIssueCommentNSID, user.Did, rkey)
			if err != nil {
				// failed to get record
				log.Println(err, rkey)
				span.RecordError(err)
				span.SetStatus(codes.Error, "failed to get record from PDS")
				s.pages.Notice(w, fmt.Sprintf("comment-%s-status", commentId), "Failed to update description, no record found on PDS.")
				return
			}
			value, _ := ex.Value.MarshalJSON() // we just did get record; it is valid json
			record, _ := data.UnmarshalJSON(value)

			repoAt := record["repo"].(string)
			issueAt := record["issue"].(string)
			createdAt := record["createdAt"].(string)
			commentIdInt64 := int64(commentIdInt)

			_, err = comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
				Collection: tangled.RepoIssueCommentNSID,
				Repo:       user.Did,
				Rkey:       rkey,
				SwapRecord: ex.Cid,
				Record: &lexutil.LexiconTypeDecoder{
					Val: &tangled.RepoIssueComment{
						Repo:      &repoAt,
						Issue:     issueAt,
						CommentId: &commentIdInt64,
						Owner:     &comment.OwnerDid,
						Body:      newBody,
						CreatedAt: createdAt,
					},
				},
			})
			if err != nil {
				log.Println(err)
				span.RecordError(err)
				span.SetStatus(codes.Error, "failed to put record to PDS")
			}
		}

		// optimistic update for htmx
		didHandleMap := map[string]string{
			user.Did: user.Handle,
		}
		comment.Body = newBody
		comment.Edited = &edited

		// return new comment body with htmx
		s.pages.SingleIssueCommentFragment(w, pages.SingleIssueCommentParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(ctx, s, user),
			DidHandleMap: didHandleMap,
			Issue:        issue,
			Comment:      comment,
		})
		return
	}
}

func (s *State) DeleteIssueComment(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "DeleteIssueComment")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	issueId := chi.URLParam(r, "issue")
	issueIdInt, err := strconv.Atoi(issueId)
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse issue id")
		return
	}

	issue, err := db.GetIssue(ctx, s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get issue")
		s.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse comment id")
		return
	}

	span.SetAttributes(
		attribute.Int("issue_id", issueIdInt),
		attribute.Int("comment_id", commentIdInt),
	)

	comment, err := db.GetComment(s.db, f.RepoAt, issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get comment")
		return
	}

	if comment.OwnerDid != user.Did {
		http.Error(w, "you are not the author of this comment", http.StatusUnauthorized)
		span.SetAttributes(attribute.Bool("permission_denied", true))
		return
	}

	if comment.Deleted != nil {
		http.Error(w, "comment already deleted", http.StatusBadRequest)
		span.SetAttributes(attribute.Bool("already_deleted", true))
		return
	}

	// optimistic deletion
	deleted := time.Now()
	err = db.DeleteComment(s.db, f.RepoAt, issueIdInt, commentIdInt)
	if err != nil {
		log.Println("failed to delete comment")
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to delete comment in database")
		s.pages.Notice(w, fmt.Sprintf("comment-%s-status", commentId), "failed to delete comment")
		return
	}

	// delete from pds
	if comment.Rkey != "" {
		client, _ := s.auth.AuthorizedClient(r)
		_, err = comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.GraphFollowNSID,
			Repo:       user.Did,
			Rkey:       comment.Rkey,
		})
		if err != nil {
			log.Println(err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to delete record from PDS")
		}
	}

	// optimistic update for htmx
	didHandleMap := map[string]string{
		user.Did: user.Handle,
	}
	comment.Body = ""
	comment.Deleted = &deleted

	// htmx fragment of comment after deletion
	s.pages.SingleIssueCommentFragment(w, pages.SingleIssueCommentParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(ctx, s, user),
		DidHandleMap: didHandleMap,
		Issue:        issue,
		Comment:      comment,
	})
	return
}

func (s *State) RepoIssues(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "RepoIssues")
	defer span.End()

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

	span.SetAttributes(
		attribute.Bool("is_open", isOpen),
		attribute.String("state_param", state),
	)

	page, ok := r.Context().Value("page").(pagination.Page)
	if !ok {
		log.Println("failed to get page")
		span.SetAttributes(attribute.Bool("page_not_found", true))
		page = pagination.FirstPage()
	}

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	issues, err := db.GetIssues(ctx, s.db, f.RepoAt, isOpen, page)
	if err != nil {
		log.Println("failed to get issues", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get issues")
		s.pages.Notice(w, "issues", "Failed to load issues. Try again later.")
		return
	}

	span.SetAttributes(attribute.Int("issues.count", len(issues)))

	identsToResolve := make([]string, len(issues))
	for i, issue := range issues {
		identsToResolve[i] = issue.OwnerDid
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

	s.pages.RepoIssues(w, pages.RepoIssuesParams{
		LoggedInUser:    s.auth.GetUser(r),
		RepoInfo:        f.RepoInfo(ctx, s, user),
		Issues:          issues,
		DidHandleMap:    didHandleMap,
		FilteringByOpen: isOpen,
		Page:            page,
	})
	return
}

func (s *State) NewIssue(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "NewIssue")
	defer span.End()

	user := s.auth.GetUser(r)

	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Println("failed to get repo and knot", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve repo")
		return
	}

	span.SetAttributes(attribute.String("method", r.Method))

	switch r.Method {
	case http.MethodGet:
		s.pages.RepoNewIssue(w, pages.RepoNewIssueParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(ctx, s, user),
		})
	case http.MethodPost:
		title := r.FormValue("title")
		body := r.FormValue("body")

		span.SetAttributes(
			attribute.String("title", title),
			attribute.String("body_length", fmt.Sprintf("%d", len(body))),
		)

		if title == "" || body == "" {
			span.SetAttributes(attribute.Bool("form_validation_failed", true))
			s.pages.Notice(w, "issues", "Title and body are required")
			return
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to begin transaction")
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
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create issue in database")
			s.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		issueId, err := db.GetIssueId(s.db, f.RepoAt)
		if err != nil {
			log.Println("failed to get issue id", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to get issue id")
			s.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		span.SetAttributes(attribute.Int("issue_id", issueId))

		client, _ := s.auth.AuthorizedClient(r)
		atUri := f.RepoAt.String()
		rkey := appview.TID()
		span.SetAttributes(attribute.String("rkey", rkey))

		resp, err := comatproto.RepoPutRecord(ctx, client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoIssueNSID,
			Repo:       user.Did,
			Rkey:       rkey,
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
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create issue in PDS")
			s.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		span.SetAttributes(attribute.String("issue_uri", resp.Uri))

		err = db.SetIssueAt(s.db, f.RepoAt, issueId, resp.Uri)
		if err != nil {
			log.Println("failed to set issue at", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to set issue URI in database")
			s.pages.Notice(w, "issues", "Failed to create issue.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d", f.OwnerSlashRepo(), issueId))
		return
	}
}

func (s *State) ForkRepo(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "ForkRepo")
	defer span.End()

	user := s.auth.GetUser(r)
	f, err := s.fullyResolvedRepo(r.WithContext(ctx))
	if err != nil {
		log.Printf("failed to resolve source repo: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to resolve source repo")
		return
	}

	span.SetAttributes(
		attribute.String("method", r.Method),
		attribute.String("repo_name", f.RepoName),
		attribute.String("owner_did", f.OwnerDid()),
		attribute.String("knot", f.Knot),
	)

	switch r.Method {
	case http.MethodGet:
		user := s.auth.GetUser(r)
		knots, err := s.enforcer.GetDomainsForUser(user.Did)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to get domains for user")
			s.pages.Notice(w, "repo", "Invalid user account.")
			return
		}

		span.SetAttributes(attribute.Int("knots.count", len(knots)))

		s.pages.ForkRepo(w, pages.ForkRepoParams{
			LoggedInUser: user,
			Knots:        knots,
			RepoInfo:     f.RepoInfo(ctx, s, user),
		})

	case http.MethodPost:
		knot := r.FormValue("knot")
		if knot == "" {
			span.SetAttributes(attribute.Bool("missing_knot", true))
			s.pages.Notice(w, "repo", "Invalid form submission&mdash;missing knot domain.")
			return
		}

		span.SetAttributes(attribute.String("target_knot", knot))

		ok, err := s.enforcer.E.Enforce(user.Did, knot, knot, "repo:create")
		if err != nil || !ok {
			span.SetAttributes(
				attribute.Bool("permission_denied", true),
				attribute.Bool("enforce_error", err != nil),
			)
			s.pages.Notice(w, "repo", "You do not have permission to create a repo in this knot.")
			return
		}

		forkName := fmt.Sprintf("%s", f.RepoName)
		span.SetAttributes(attribute.String("fork_name", forkName))

		// this check is *only* to see if the forked repo name already exists
		// in the user's account.
		existingRepo, err := db.GetRepo(ctx, s.db, user.Did, f.RepoName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// no existing repo with this name found, we can use the name as is
				span.SetAttributes(attribute.Bool("repo_name_available", true))
			} else {
				log.Println("error fetching existing repo from db", err)
				span.RecordError(err)
				span.SetStatus(codes.Error, "failed to check for existing repo")
				s.pages.Notice(w, "repo", "Failed to fork this repository. Try again later.")
				return
			}
		} else if existingRepo != nil {
			// repo with this name already exists, append random string
			forkName = fmt.Sprintf("%s-%s", forkName, randomString(3))
			span.SetAttributes(
				attribute.Bool("repo_name_conflict", true),
				attribute.String("adjusted_fork_name", forkName),
			)
		}

		secret, err := db.GetRegistrationKey(s.db, knot)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to get registration key")
			s.pages.Notice(w, "repo", fmt.Sprintf("No registration key found for knot %s.", knot))
			return
		}

		client, err := NewSignedClient(knot, secret, s.config.Dev)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create signed client")
			s.pages.Notice(w, "repo", "Failed to reach knot server.")
			return
		}

		var uri string
		if s.config.Dev {
			uri = "http"
		} else {
			uri = "https"
		}
		forkSourceUrl := fmt.Sprintf("%s://%s/%s/%s", uri, f.Knot, f.OwnerDid(), f.RepoName)
		sourceAt := f.RepoAt.String()

		span.SetAttributes(
			attribute.String("fork_source_url", forkSourceUrl),
			attribute.String("source_at", sourceAt),
		)

		rkey := appview.TID()
		repo := &db.Repo{
			Did:    user.Did,
			Name:   forkName,
			Knot:   knot,
			Rkey:   rkey,
			Source: sourceAt,
		}

		span.SetAttributes(attribute.String("rkey", rkey))

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			log.Println(err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to begin transaction")
			s.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}
		defer func() {
			tx.Rollback()
			err = s.enforcer.E.LoadPolicy()
			if err != nil {
				log.Println("failed to rollback policies")
				span.RecordError(err)
			}
		}()

		resp, err := client.ForkRepo(user.Did, forkSourceUrl, forkName)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to fork repo on knot server")
			s.pages.Notice(w, "repo", "Failed to create repository on knot server.")
			return
		}

		span.SetAttributes(attribute.Int("fork_response_status", resp.StatusCode))

		switch resp.StatusCode {
		case http.StatusConflict:
			span.SetAttributes(attribute.Bool("name_conflict", true))
			s.pages.Notice(w, "repo", "A repository with that name already exists.")
			return
		case http.StatusInternalServerError:
			span.SetAttributes(attribute.Bool("server_error", true))
			s.pages.Notice(w, "repo", "Failed to create repository on knot. Try again later.")
			return
		case http.StatusNoContent:
			// continue
		}

		xrpcClient, _ := s.auth.AuthorizedClient(r)

		createdAt := time.Now().Format(time.RFC3339)
		atresp, err := comatproto.RepoPutRecord(ctx, xrpcClient, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.Repo{
					Knot:      repo.Knot,
					Name:      repo.Name,
					CreatedAt: createdAt,
					Owner:     user.Did,
					Source:    &sourceAt,
				}},
		})
		if err != nil {
			log.Printf("failed to create record: %s", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create record in PDS")
			s.pages.Notice(w, "repo", "Failed to announce repository creation.")
			return
		}
		log.Println("created repo record: ", atresp.Uri)
		span.SetAttributes(attribute.String("repo_uri", atresp.Uri))

		repo.AtUri = atresp.Uri
		err = db.AddRepo(ctx, tx, repo)
		if err != nil {
			log.Println(err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to add repo to database")
			s.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// acls
		p, _ := securejoin.SecureJoin(user.Did, forkName)
		err = s.enforcer.AddRepo(user.Did, knot, p)
		if err != nil {
			log.Println(err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to set up repository permissions")
			s.pages.Notice(w, "repo", "Failed to set up repository permissions.")
			return
		}

		err = tx.Commit()
		if err != nil {
			log.Println("failed to commit changes", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to commit transaction")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = s.enforcer.E.SavePolicy()
		if err != nil {
			log.Println("failed to update ACLs", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to save policy")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s", user.Handle, forkName))
		return
	}
}
