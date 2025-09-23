package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/commitverify"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/appview/reporesolver"
	"tangled.org/core/appview/validator"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/eventconsumer"
	"tangled.org/core/idresolver"
	"tangled.org/core/patchutil"
	"tangled.org/core/rbac"
	"tangled.org/core/tid"
	"tangled.org/core/types"
	"tangled.org/core/xrpc/serviceauth"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	atpclient "github.com/bluesky-social/indigo/atproto/client"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

type Repo struct {
	repoResolver  *reporesolver.RepoResolver
	idResolver    *idresolver.Resolver
	config        *config.Config
	oauth         *oauth.OAuth
	pages         *pages.Pages
	spindlestream *eventconsumer.Consumer
	db            *db.DB
	enforcer      *rbac.Enforcer
	notifier      notify.Notifier
	logger        *slog.Logger
	serviceAuth   *serviceauth.ServiceAuth
	validator     *validator.Validator
}

func New(
	oauth *oauth.OAuth,
	repoResolver *reporesolver.RepoResolver,
	pages *pages.Pages,
	spindlestream *eventconsumer.Consumer,
	idResolver *idresolver.Resolver,
	db *db.DB,
	config *config.Config,
	notifier notify.Notifier,
	enforcer *rbac.Enforcer,
	logger *slog.Logger,
	validator *validator.Validator,
) *Repo {
	return &Repo{oauth: oauth,
		repoResolver:  repoResolver,
		pages:         pages,
		idResolver:    idResolver,
		config:        config,
		spindlestream: spindlestream,
		db:            db,
		notifier:      notifier,
		enforcer:      enforcer,
		logger:        logger,
		validator:     validator,
	}
}

func (rp *Repo) DownloadArchive(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "DownloadArchive")

	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	archiveBytes, err := tangled.RepoArchive(r.Context(), xrpcc, "tar.gz", "", ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.archive", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	// Set headers for file download, just pass along whatever the knot specifies
	safeRefFilename := strings.ReplaceAll(plumbing.ReferenceName(ref).Short(), "/", "-")
	filename := fmt.Sprintf("%s-%s.tar.gz", f.Name, safeRefFilename)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(archiveBytes)))

	// Write the archive data directly
	w.Write(archiveBytes)
}

func (rp *Repo) RepoLog(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoLog")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to fully resolve repo", "err", err)
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
	ref, _ = url.PathUnescape(ref)

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	limit := int64(60)
	cursor := ""
	if page > 1 {
		// Convert page number to cursor (offset)
		offset := (page - 1) * int(limit)
		cursor = strconv.Itoa(offset)
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcBytes, err := tangled.RepoLog(r.Context(), xrpcc, cursor, limit, "", ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.log", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var xrpcResp types.RepoLogResponse
	if err := json.Unmarshal(xrpcBytes, &xrpcResp); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}

	tagBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tags", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	tagMap := make(map[string][]string)
	if tagBytes != nil {
		var tagResp types.RepoTagsResponse
		if err := json.Unmarshal(tagBytes, &tagResp); err == nil {
			for _, tag := range tagResp.Tags {
				tagMap[tag.Hash] = append(tagMap[tag.Hash], tag.Name)
			}
		}
	}

	branchBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	if branchBytes != nil {
		var branchResp types.RepoBranchesResponse
		if err := json.Unmarshal(branchBytes, &branchResp); err == nil {
			for _, branch := range branchResp.Branches {
				tagMap[branch.Hash] = append(tagMap[branch.Hash], branch.Name)
			}
		}
	}

	user := rp.oauth.GetUser(r)

	emailToDidMap, err := db.GetEmailToDid(rp.db, uniqueEmails(xrpcResp.Commits), true)
	if err != nil {
		l.Error("failed to fetch email to did mapping", "err", err)
	}

	vc, err := commitverify.GetVerifiedObjectCommits(rp.db, emailToDidMap, xrpcResp.Commits)
	if err != nil {
		l.Error("failed to GetVerifiedObjectCommits", "err", err)
	}

	repoInfo := f.RepoInfo(user)

	var shas []string
	for _, c := range xrpcResp.Commits {
		shas = append(shas, c.Hash.String())
	}
	pipelines, err := getPipelineStatuses(rp.db, repoInfo, shas)
	if err != nil {
		l.Error("failed to getPipelineStatuses", "err", err)
		// non-fatal
	}

	rp.pages.RepoLog(w, pages.RepoLogParams{
		LoggedInUser:       user,
		TagMap:             tagMap,
		RepoInfo:           repoInfo,
		RepoLogResponse:    xrpcResp,
		EmailToDidOrHandle: emailToDidOrHandle(rp, emailToDidMap),
		VerifiedCommits:    vc,
		Pipelines:          pipelines,
	})
}

func (rp *Repo) RepoDescriptionEdit(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoDescriptionEdit")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	user := rp.oauth.GetUser(r)
	rp.pages.EditRepoDescriptionFragment(w, pages.RepoDescriptionParams{
		RepoInfo: f.RepoInfo(user),
	})
}

func (rp *Repo) RepoDescription(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoDescription")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	repoAt := f.RepoAt()
	rkey := repoAt.RecordKey().String()
	if rkey == "" {
		l.Error("invalid aturi for repo", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	user := rp.oauth.GetUser(r)

	switch r.Method {
	case http.MethodGet:
		rp.pages.RepoDescriptionFragment(w, pages.RepoDescriptionParams{
			RepoInfo: f.RepoInfo(user),
		})
		return
	case http.MethodPut:
		newDescription := r.FormValue("description")
		client, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to get client")
			rp.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// optimistic update
		err = db.UpdateDescription(rp.db, string(repoAt), newDescription)
		if err != nil {
			l.Error("failed to perform update-description query", "err", err)
			rp.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		newRepo := f.Repo
		newRepo.Description = newDescription
		record := newRepo.AsRecord()

		// this is a bit of a pain because the golang atproto impl does not allow nil SwapRecord field
		//
		// SwapRecord is optional and should happen automagically, but given that it does not, we have to perform two requests
		ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, newRepo.Did, newRepo.Rkey)
		if err != nil {
			// failed to get record
			rp.pages.Notice(w, "repo-notice", "Failed to update description, no record found on PDS.")
			return
		}
		_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       newRepo.Did,
			Rkey:       newRepo.Rkey,
			SwapRecord: ex.Cid,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &record,
			},
		})

		if err != nil {
			l.Error("failed to perferom update-description query", "err", err)
			// failed to get record
			rp.pages.Notice(w, "repo-notice", "Failed to update description, unable to save to PDS.")
			return
		}

		newRepoInfo := f.RepoInfo(user)
		newRepoInfo.Description = newDescription

		rp.pages.RepoDescriptionFragment(w, pages.RepoDescriptionParams{
			RepoInfo: newRepoInfo,
		})
		return
	}
}

func (rp *Repo) RepoCommit(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoCommit")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to fully resolve repo", "err", err)
		return
	}
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	var diffOpts types.DiffOpts
	if d := r.URL.Query().Get("diff"); d == "split" {
		diffOpts.Split = true
	}

	if !plumbing.IsHash(ref) {
		rp.pages.Error404(w)
		return
	}

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcBytes, err := tangled.RepoDiff(r.Context(), xrpcc, ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.diff", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoCommitResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}

	emailToDidMap, err := db.GetEmailToDid(rp.db, []string{result.Diff.Commit.Committer.Email, result.Diff.Commit.Author.Email}, true)
	if err != nil {
		l.Error("failed to get email to did mapping", "err", err)
	}

	vc, err := commitverify.GetVerifiedCommits(rp.db, emailToDidMap, []types.NiceDiff{*result.Diff})
	if err != nil {
		l.Error("failed to GetVerifiedCommits", "err", err)
	}

	user := rp.oauth.GetUser(r)
	repoInfo := f.RepoInfo(user)
	pipelines, err := getPipelineStatuses(rp.db, repoInfo, []string{result.Diff.Commit.This})
	if err != nil {
		l.Error("failed to getPipelineStatuses", "err", err)
		// non-fatal
	}
	var pipeline *models.Pipeline
	if p, ok := pipelines[result.Diff.Commit.This]; ok {
		pipeline = &p
	}

	rp.pages.RepoCommit(w, pages.RepoCommitParams{
		LoggedInUser:       user,
		RepoInfo:           f.RepoInfo(user),
		RepoCommitResponse: result,
		EmailToDidOrHandle: emailToDidOrHandle(rp, emailToDidMap),
		VerifiedCommit:     vc,
		Pipeline:           pipeline,
		DiffOpts:           diffOpts,
	})
}

func (rp *Repo) RepoTree(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoTree")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to fully resolve repo", "err", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	// if the tree path has a trailing slash, let's strip it
	// so we don't 404
	treePath := chi.URLParam(r, "*")
	treePath, _ = url.PathUnescape(treePath)
	treePath = strings.TrimSuffix(treePath, "/")

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcResp, err := tangled.RepoTree(r.Context(), xrpcc, treePath, ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tree", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	// Convert XRPC response to internal types.RepoTreeResponse
	files := make([]types.NiceTree, len(xrpcResp.Files))
	for i, xrpcFile := range xrpcResp.Files {
		file := types.NiceTree{
			Name:      xrpcFile.Name,
			Mode:      xrpcFile.Mode,
			Size:      int64(xrpcFile.Size),
			IsFile:    xrpcFile.Is_file,
			IsSubtree: xrpcFile.Is_subtree,
		}

		// Convert last commit info if present
		if xrpcFile.Last_commit != nil {
			commitWhen, _ := time.Parse(time.RFC3339, xrpcFile.Last_commit.When)
			file.LastCommit = &types.LastCommitInfo{
				Hash:    plumbing.NewHash(xrpcFile.Last_commit.Hash),
				Message: xrpcFile.Last_commit.Message,
				When:    commitWhen,
			}
		}

		files[i] = file
	}

	result := types.RepoTreeResponse{
		Ref:   xrpcResp.Ref,
		Files: files,
	}

	if xrpcResp.Parent != nil {
		result.Parent = *xrpcResp.Parent
	}
	if xrpcResp.Dotdot != nil {
		result.DotDot = *xrpcResp.Dotdot
	}
	if xrpcResp.Readme != nil {
		result.ReadmeFileName = xrpcResp.Readme.Filename
		result.Readme = xrpcResp.Readme.Contents
	}

	// redirects tree paths trying to access a blob; in this case the result.Files is unpopulated,
	// so we can safely redirect to the "parent" (which is the same file).
	if len(result.Files) == 0 && result.Parent == treePath {
		redirectTo := fmt.Sprintf("/%s/blob/%s/%s", f.OwnerSlashRepo(), url.PathEscape(ref), result.Parent)
		http.Redirect(w, r, redirectTo, http.StatusFound)
		return
	}

	user := rp.oauth.GetUser(r)

	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.Name, fmt.Sprintf("/%s/tree/%s", f.OwnerSlashRepo(), url.PathEscape(ref))})
	if treePath != "" {
		for idx, elem := range strings.Split(treePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], url.PathEscape(elem))})
		}
	}

	sortFiles(result.Files)

	rp.pages.RepoTree(w, pages.RepoTreeParams{
		LoggedInUser:     user,
		BreadCrumbs:      breadcrumbs,
		TreePath:         treePath,
		RepoInfo:         f.RepoInfo(user),
		RepoTreeResponse: result,
	})
}

func (rp *Repo) RepoTags(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoTags")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tags", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoTagsResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}

	artifacts, err := db.GetArtifact(rp.db, db.FilterEq("repo_at", f.RepoAt()))
	if err != nil {
		l.Error("failed grab artifacts", "err", err)
		return
	}

	// convert artifacts to map for easy UI building
	artifactMap := make(map[plumbing.Hash][]models.Artifact)
	for _, a := range artifacts {
		artifactMap[a.Tag] = append(artifactMap[a.Tag], a)
	}

	var danglingArtifacts []models.Artifact
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

	user := rp.oauth.GetUser(r)
	rp.pages.RepoTags(w, pages.RepoTagsParams{
		LoggedInUser:      user,
		RepoInfo:          f.RepoInfo(user),
		RepoTagsResponse:  result,
		ArtifactMap:       artifactMap,
		DanglingArtifacts: danglingArtifacts,
	})
}

func (rp *Repo) RepoBranches(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoBranches")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoBranchesResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}

	sortBranches(result.Branches)

	user := rp.oauth.GetUser(r)
	rp.pages.RepoBranches(w, pages.RepoBranchesParams{
		LoggedInUser:         user,
		RepoInfo:             f.RepoInfo(user),
		RepoBranchesResponse: result,
	})
}

func (rp *Repo) DeleteBranch(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "DeleteBranch")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	noticeId := "delete-branch-error"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, noticeId, msg)
	}

	branch := r.FormValue("branch")
	if branch == "" {
		fail("No branch provided.", nil)
		return
	}

	client, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoDeleteBranchNSID),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		fail("Failed to connect to knotserver", nil)
		return
	}

	err = tangled.RepoDeleteBranch(
		r.Context(),
		client,
		&tangled.RepoDeleteBranch_Input{
			Branch: branch,
			Repo:   f.RepoAt().String(),
		},
	)
	if err := xrpcclient.HandleXrpcErr(err); err != nil {
		fail(fmt.Sprintf("Failed to delete branch: %s", err), err)
		return
	}
	l.Error("deleted branch from knot", "branch", branch, "repo", f.RepoAt())

	rp.pages.HxRefresh(w)
}

func (rp *Repo) RepoBlob(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoBlob")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	filePath := chi.URLParam(r, "*")
	filePath, _ = url.PathUnescape(filePath)

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Repo.Name)
	resp, err := tangled.RepoBlob(r.Context(), xrpcc, filePath, false, ref, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.blob", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	// Use XRPC response directly instead of converting to internal types

	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.Name, fmt.Sprintf("/%s/tree/%s", f.OwnerSlashRepo(), url.PathEscape(ref))})
	if filePath != "" {
		for idx, elem := range strings.Split(filePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], url.PathEscape(elem))})
		}
	}

	showRendered := false
	renderToggle := false

	if markup.GetFormat(resp.Path) == markup.FormatMarkdown {
		renderToggle = true
		showRendered = r.URL.Query().Get("code") != "true"
	}

	var unsupported bool
	var isImage bool
	var isVideo bool
	var contentSrc string

	if resp.IsBinary != nil && *resp.IsBinary {
		ext := strings.ToLower(filepath.Ext(resp.Path))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp":
			isImage = true
		case ".mp4", ".webm", ".ogg", ".mov", ".avi":
			isVideo = true
		default:
			unsupported = true
		}

		// fetch the raw binary content using sh.tangled.repo.blob xrpc
		repoName := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)

		baseURL := &url.URL{
			Scheme: scheme,
			Host:   f.Knot,
			Path:   "/xrpc/sh.tangled.repo.blob",
		}
		query := baseURL.Query()
		query.Set("repo", repoName)
		query.Set("ref", ref)
		query.Set("path", filePath)
		query.Set("raw", "true")
		baseURL.RawQuery = query.Encode()
		blobURL := baseURL.String()

		contentSrc = blobURL
		if !rp.config.Core.Dev {
			contentSrc = markup.GenerateCamoURL(rp.config.Camo.Host, rp.config.Camo.SharedSecret, blobURL)
		}
	}

	lines := 0
	if resp.IsBinary == nil || !*resp.IsBinary {
		lines = strings.Count(resp.Content, "\n") + 1
	}

	var sizeHint uint64
	if resp.Size != nil {
		sizeHint = uint64(*resp.Size)
	} else {
		sizeHint = uint64(len(resp.Content))
	}

	user := rp.oauth.GetUser(r)

	// Determine if content is binary (dereference pointer)
	isBinary := false
	if resp.IsBinary != nil {
		isBinary = *resp.IsBinary
	}

	rp.pages.RepoBlob(w, pages.RepoBlobParams{
		LoggedInUser:    user,
		RepoInfo:        f.RepoInfo(user),
		BreadCrumbs:     breadcrumbs,
		ShowRendered:    showRendered,
		RenderToggle:    renderToggle,
		Unsupported:     unsupported,
		IsImage:         isImage,
		IsVideo:         isVideo,
		ContentSrc:      contentSrc,
		RepoBlob_Output: resp,
		Contents:        resp.Content,
		Lines:           lines,
		SizeHint:        sizeHint,
		IsBinary:        isBinary,
	})
}

func (rp *Repo) RepoBlobRaw(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoBlobRaw")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	filePath := chi.URLParam(r, "*")
	filePath, _ = url.PathUnescape(filePath)

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Repo.Name)
	baseURL := &url.URL{
		Scheme: scheme,
		Host:   f.Knot,
		Path:   "/xrpc/sh.tangled.repo.blob",
	}
	query := baseURL.Query()
	query.Set("repo", repo)
	query.Set("ref", ref)
	query.Set("path", filePath)
	query.Set("raw", "true")
	baseURL.RawQuery = query.Encode()
	blobURL := baseURL.String()

	req, err := http.NewRequest("GET", blobURL, nil)
	if err != nil {
		l.Error("failed to create request", "err", err)
		return
	}

	// forward the If-None-Match header
	if clientETag := r.Header.Get("If-None-Match"); clientETag != "" {
		req.Header.Set("If-None-Match", clientETag)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		l.Error("failed to reach knotserver", "err", err)
		rp.pages.Error503(w)
		return
	}
	defer resp.Body.Close()

	// forward 304 not modified
	if resp.StatusCode == http.StatusNotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if resp.StatusCode != http.StatusOK {
		l.Error("knotserver returned non-OK status for raw blob", "url", blobURL, "statuscode", resp.StatusCode)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.Error("error reading response body from knotserver", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if strings.HasPrefix(contentType, "text/") || isTextualMimeType(contentType) {
		// serve all textual content as text/plain
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(body)
	} else if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") {
		// serve images and videos with their original content type
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	} else {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		w.Write([]byte("unsupported content type"))
		return
	}
}

// isTextualMimeType returns true if the MIME type represents textual content
// that should be served as text/plain
func isTextualMimeType(mimeType string) bool {
	textualTypes := []string{
		"application/json",
		"application/xml",
		"application/yaml",
		"application/x-yaml",
		"application/toml",
		"application/javascript",
		"application/ecmascript",
		"message/",
	}

	return slices.Contains(textualTypes, mimeType)
}

// modify the spindle configured for this repo
func (rp *Repo) EditSpindle(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "EditSpindle")
	l = l.With("did", user.Did)

	errorId := "operation-error"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		fail("Failed to resolve repo. Try again later", err)
		return
	}

	newSpindle := r.FormValue("spindle")
	removingSpindle := newSpindle == "[[none]]" // see pages/templates/repo/settings/pipelines.html for more info on why we use this value
	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail("Failed to authorize. Try again later.", err)
		return
	}

	if !removingSpindle {
		// ensure that this is a valid spindle for this user
		validSpindles, err := rp.enforcer.GetSpindlesForUser(user.Did)
		if err != nil {
			fail("Failed to find spindles. Try again later.", err)
			return
		}

		if !slices.Contains(validSpindles, newSpindle) {
			fail("Failed to configure spindle.", fmt.Errorf("%s is not a valid spindle: %q", newSpindle, validSpindles))
			return
		}
	}

	newRepo := f.Repo
	newRepo.Spindle = newSpindle
	record := newRepo.AsRecord()

	spindlePtr := &newSpindle
	if removingSpindle {
		spindlePtr = nil
		newRepo.Spindle = ""
	}

	// optimistic update
	err = db.UpdateSpindle(rp.db, newRepo.RepoAt().String(), spindlePtr)
	if err != nil {
		fail("Failed to update spindle. Try again later.", err)
		return
	}

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, newRepo.Did, newRepo.Rkey)
	if err != nil {
		fail("Failed to update spindle, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &record,
		},
	})

	if err != nil {
		fail("Failed to update spindle, unable to save to PDS.", err)
		return
	}

	if !removingSpindle {
		// add this spindle to spindle stream
		rp.spindlestream.AddSource(
			context.Background(),
			eventconsumer.NewSpindleSource(newSpindle),
		)
	}

	rp.pages.HxRefresh(w)
}

func (rp *Repo) AddLabelDef(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "AddLabel")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	errorId := "add-label-error"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	// get form values for label definition
	name := r.FormValue("name")
	concreteType := r.FormValue("valueType")
	valueFormat := r.FormValue("valueFormat")
	enumValues := r.FormValue("enumValues")
	scope := r.Form["scope"]
	color := r.FormValue("color")
	multiple := r.FormValue("multiple") == "true"

	var variants []string
	for part := range strings.SplitSeq(enumValues, ",") {
		if part = strings.TrimSpace(part); part != "" {
			variants = append(variants, part)
		}
	}

	if concreteType == "" {
		concreteType = "null"
	}

	format := models.ValueTypeFormatAny
	if valueFormat == "did" {
		format = models.ValueTypeFormatDid
	}

	valueType := models.ValueType{
		Type:   models.ConcreteType(concreteType),
		Format: format,
		Enum:   variants,
	}

	label := models.LabelDefinition{
		Did:       user.Did,
		Rkey:      tid.TID(),
		Name:      name,
		ValueType: valueType,
		Scope:     scope,
		Color:     &color,
		Multiple:  multiple,
		Created:   time.Now(),
	}
	if err := rp.validator.ValidateLabelDefinition(&label); err != nil {
		fail(err.Error(), err)
		return
	}

	// announce this relation into the firehose, store into owners' pds
	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail(err.Error(), err)
		return
	}

	// emit a labelRecord
	labelRecord := label.AsRecord()
	resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.LabelDefinitionNSID,
		Repo:       label.Did,
		Rkey:       label.Rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &labelRecord,
		},
	})
	// invalid record
	if err != nil {
		fail("Failed to write record to PDS.", err)
		return
	}

	aturi := resp.Uri
	l = l.With("at-uri", aturi)
	l.Info("wrote label record to PDS")

	// update the repo to subscribe to this label
	newRepo := f.Repo
	newRepo.Labels = append(newRepo.Labels, aturi)
	repoRecord := newRepo.AsRecord()

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, newRepo.Did, newRepo.Rkey)
	if err != nil {
		fail("Failed to update labels, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &repoRecord,
		},
	})
	if err != nil {
		fail("Failed to update labels for repo.", err)
		return
	}

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail("Failed to add label.", err)
		return
	}

	rollback := func() {
		err1 := tx.Rollback()
		err2 := rollbackRecord(context.Background(), aturi, client)

		// ignore txn complete errors, this is okay
		if errors.Is(err1, sql.ErrTxDone) {
			err1 = nil
		}

		if errs := errors.Join(err1, err2); errs != nil {
			l.Error("failed to rollback changes", "errs", errs)
			return
		}
	}
	defer rollback()

	_, err = db.AddLabelDefinition(tx, &label)
	if err != nil {
		fail("Failed to add label.", err)
		return
	}

	err = db.SubscribeLabel(tx, &models.RepoLabel{
		RepoAt:  f.RepoAt(),
		LabelAt: label.AtUri(),
	})

	err = tx.Commit()
	if err != nil {
		fail("Failed to add label.", err)
		return
	}

	// clear aturi when everything is successful
	aturi = ""

	rp.pages.HxRefresh(w)
}

func (rp *Repo) DeleteLabelDef(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "DeleteLabel")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	errorId := "label-operation"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	// get form values
	labelId := r.FormValue("label-id")

	label, err := db.GetLabelDefinition(rp.db, db.FilterEq("id", labelId))
	if err != nil {
		fail("Failed to find label definition.", err)
		return
	}

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail(err.Error(), err)
		return
	}

	// delete label record from PDS
	_, err = comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.LabelDefinitionNSID,
		Repo:       label.Did,
		Rkey:       label.Rkey,
	})
	if err != nil {
		fail("Failed to delete label record from PDS.", err)
		return
	}

	// update repo record to remove the label reference
	newRepo := f.Repo
	var updated []string
	removedAt := label.AtUri().String()
	for _, l := range newRepo.Labels {
		if l != removedAt {
			updated = append(updated, l)
		}
	}
	newRepo.Labels = updated
	repoRecord := newRepo.AsRecord()

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, newRepo.Did, newRepo.Rkey)
	if err != nil {
		fail("Failed to update labels, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &repoRecord,
		},
	})
	if err != nil {
		fail("Failed to update repo record.", err)
		return
	}

	// transaction for DB changes
	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail("Failed to delete label.", err)
		return
	}
	defer tx.Rollback()

	err = db.UnsubscribeLabel(
		tx,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterEq("label_at", removedAt),
	)
	if err != nil {
		fail("Failed to unsubscribe label.", err)
		return
	}

	err = db.DeleteLabelDefinition(tx, db.FilterEq("id", label.Id))
	if err != nil {
		fail("Failed to delete label definition.", err)
		return
	}

	err = tx.Commit()
	if err != nil {
		fail("Failed to delete label.", err)
		return
	}

	// everything succeeded
	rp.pages.HxRefresh(w)
}

func (rp *Repo) SubscribeLabel(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "SubscribeLabel")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	if err := r.ParseForm(); err != nil {
		l.Error("invalid form", "err", err)
		return
	}

	errorId := "default-label-operation"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	labelAts := r.Form["label"]
	_, err = db.GetLabelDefinitions(rp.db, db.FilterIn("at_uri", labelAts))
	if err != nil {
		fail("Failed to subscribe to label.", err)
		return
	}

	newRepo := f.Repo
	newRepo.Labels = append(newRepo.Labels, labelAts...)

	// dedup
	slices.Sort(newRepo.Labels)
	newRepo.Labels = slices.Compact(newRepo.Labels)

	repoRecord := newRepo.AsRecord()

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail(err.Error(), err)
		return
	}

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, f.Repo.Did, f.Repo.Rkey)
	if err != nil {
		fail("Failed to update labels, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &repoRecord,
		},
	})

	tx, err := rp.db.Begin()
	if err != nil {
		fail("Failed to subscribe to label.", err)
		return
	}
	defer tx.Rollback()

	for _, l := range labelAts {
		err = db.SubscribeLabel(tx, &models.RepoLabel{
			RepoAt:  f.RepoAt(),
			LabelAt: syntax.ATURI(l),
		})
		if err != nil {
			fail("Failed to subscribe to label.", err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		fail("Failed to subscribe to label.", err)
		return
	}

	// everything succeeded
	rp.pages.HxRefresh(w)
}

func (rp *Repo) UnsubscribeLabel(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "UnsubscribeLabel")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	if err := r.ParseForm(); err != nil {
		l.Error("invalid form", "err", err)
		return
	}

	errorId := "default-label-operation"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	labelAts := r.Form["label"]
	_, err = db.GetLabelDefinitions(rp.db, db.FilterIn("at_uri", labelAts))
	if err != nil {
		fail("Failed to unsubscribe to label.", err)
		return
	}

	// update repo record to remove the label reference
	newRepo := f.Repo
	var updated []string
	for _, l := range newRepo.Labels {
		if !slices.Contains(labelAts, l) {
			updated = append(updated, l)
		}
	}
	newRepo.Labels = updated
	repoRecord := newRepo.AsRecord()

	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail(err.Error(), err)
		return
	}

	ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoNSID, f.Repo.Did, f.Repo.Rkey)
	if err != nil {
		fail("Failed to update labels, no record found on PDS.", err)
		return
	}
	_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       newRepo.Did,
		Rkey:       newRepo.Rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &repoRecord,
		},
	})

	err = db.UnsubscribeLabel(
		rp.db,
		db.FilterEq("repo_at", f.RepoAt()),
		db.FilterIn("label_at", labelAts),
	)
	if err != nil {
		fail("Failed to unsubscribe label.", err)
		return
	}

	// everything succeeded
	rp.pages.HxRefresh(w)
}

func (rp *Repo) LabelPanel(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "LabelPanel")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	subjectStr := r.FormValue("subject")
	subject, err := syntax.ParseATURI(subjectStr)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	labelDefs, err := db.GetLabelDefinitions(
		rp.db,
		db.FilterIn("at_uri", f.Repo.Labels),
		db.FilterContains("scope", subject.Collection().String()),
	)
	if err != nil {
		l.Error("failed to fetch label defs", "err", err)
		return
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labelDefs {
		defs[l.AtUri().String()] = &l
	}

	states, err := db.GetLabels(rp.db, db.FilterEq("subject", subject))
	if err != nil {
		l.Error("failed to build label state", "err", err)
		return
	}
	state := states[subject]

	user := rp.oauth.GetUser(r)
	rp.pages.LabelPanel(w, pages.LabelPanelParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Defs:         defs,
		Subject:      subject.String(),
		State:        state,
	})
}

func (rp *Repo) EditLabelPanel(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "EditLabelPanel")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	subjectStr := r.FormValue("subject")
	subject, err := syntax.ParseATURI(subjectStr)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	labelDefs, err := db.GetLabelDefinitions(
		rp.db,
		db.FilterIn("at_uri", f.Repo.Labels),
		db.FilterContains("scope", subject.Collection().String()),
	)
	if err != nil {
		l.Error("failed to fetch labels", "err", err)
		return
	}

	defs := make(map[string]*models.LabelDefinition)
	for _, l := range labelDefs {
		defs[l.AtUri().String()] = &l
	}

	states, err := db.GetLabels(rp.db, db.FilterEq("subject", subject))
	if err != nil {
		l.Error("failed to build label state", "err", err)
		return
	}
	state := states[subject]

	user := rp.oauth.GetUser(r)
	rp.pages.EditLabelPanel(w, pages.EditLabelPanelParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Defs:         defs,
		Subject:      subject.String(),
		State:        state,
	})
}

func (rp *Repo) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "AddCollaborator")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	errorId := "add-collaborator-error"
	fail := func(msg string, err error) {
		l.Error(msg, "err", err)
		rp.pages.Notice(w, errorId, msg)
	}

	collaborator := r.FormValue("collaborator")
	if collaborator == "" {
		fail("Invalid form.", nil)
		return
	}

	// remove a single leading `@`, to make @handle work with ResolveIdent
	collaborator = strings.TrimPrefix(collaborator, "@")

	collaboratorIdent, err := rp.idResolver.ResolveIdent(r.Context(), collaborator)
	if err != nil {
		fail(fmt.Sprintf("'%s' is not a valid DID/handle.", collaborator), err)
		return
	}

	if collaboratorIdent.DID.String() == user.Did {
		fail("You seem to be adding yourself as a collaborator.", nil)
		return
	}
	l = l.With("collaborator", collaboratorIdent.Handle)
	l = l.With("knot", f.Knot)

	// announce this relation into the firehose, store into owners' pds
	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		fail("Failed to write to PDS.", err)
		return
	}

	// emit a record
	currentUser := rp.oauth.GetUser(r)
	rkey := tid.TID()
	createdAt := time.Now()
	resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoCollaboratorNSID,
		Repo:       currentUser.Did,
		Rkey:       rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.RepoCollaborator{
				Subject:   collaboratorIdent.DID.String(),
				Repo:      string(f.RepoAt()),
				CreatedAt: createdAt.Format(time.RFC3339),
			}},
	})
	// invalid record
	if err != nil {
		fail("Failed to write record to PDS.", err)
		return
	}

	aturi := resp.Uri
	l = l.With("at-uri", aturi)
	l.Info("wrote record to PDS")

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail("Failed to add collaborator.", err)
		return
	}

	rollback := func() {
		err1 := tx.Rollback()
		err2 := rp.enforcer.E.LoadPolicy()
		err3 := rollbackRecord(context.Background(), aturi, client)

		// ignore txn complete errors, this is okay
		if errors.Is(err1, sql.ErrTxDone) {
			err1 = nil
		}

		if errs := errors.Join(err1, err2, err3); errs != nil {
			l.Error("failed to rollback changes", "errs", errs)
			return
		}
	}
	defer rollback()

	err = rp.enforcer.AddCollaborator(collaboratorIdent.DID.String(), f.Knot, f.DidSlashRepo())
	if err != nil {
		fail("Failed to add collaborator permissions.", err)
		return
	}

	err = db.AddCollaborator(tx, models.Collaborator{
		Did:        syntax.DID(currentUser.Did),
		Rkey:       rkey,
		SubjectDid: collaboratorIdent.DID,
		RepoAt:     f.RepoAt(),
		Created:    createdAt,
	})
	if err != nil {
		fail("Failed to add collaborator.", err)
		return
	}

	err = tx.Commit()
	if err != nil {
		fail("Failed to add collaborator.", err)
		return
	}

	err = rp.enforcer.E.SavePolicy()
	if err != nil {
		fail("Failed to update collaborator permissions.", err)
		return
	}

	// clear aturi to when everything is successful
	aturi = ""

	rp.pages.HxRefresh(w)
}

func (rp *Repo) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "DeleteRepo")

	noticeId := "operation-error"
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	// remove record from pds
	atpClient, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		l.Error("failed to get authorized client", "err", err)
		return
	}
	_, err = comatproto.RepoDeleteRecord(r.Context(), atpClient, &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       user.Did,
		Rkey:       f.Rkey,
	})
	if err != nil {
		l.Error("failed to delete record", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to delete repository from PDS.")
		return
	}
	l.Info("removed repo record", "aturi", f.RepoAt().String())

	client, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoDeleteNSID),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		l.Error("failed to connect to knot server", "err", err)
		return
	}

	err = tangled.RepoDelete(
		r.Context(),
		client,
		&tangled.RepoDelete_Input{
			Did:  f.OwnerDid(),
			Name: f.Name,
			Rkey: f.Rkey,
		},
	)
	if err := xrpcclient.HandleXrpcErr(err); err != nil {
		rp.pages.Notice(w, noticeId, err.Error())
		return
	}
	l.Info("deleted repo from knot")

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		l.Error("failed to start tx")
		w.Write(fmt.Append(nil, "failed to add collaborator: ", err))
		return
	}
	defer func() {
		tx.Rollback()
		err = rp.enforcer.E.LoadPolicy()
		if err != nil {
			l.Error("failed to rollback policies")
		}
	}()

	// remove collaborator RBAC
	repoCollaborators, err := rp.enforcer.E.GetImplicitUsersForResourceByDomain(f.DidSlashRepo(), f.Knot)
	if err != nil {
		rp.pages.Notice(w, noticeId, "Failed to remove collaborators")
		return
	}
	for _, c := range repoCollaborators {
		did := c[0]
		rp.enforcer.RemoveCollaborator(did, f.Knot, f.DidSlashRepo())
	}
	l.Info("removed collaborators")

	// remove repo RBAC
	err = rp.enforcer.RemoveRepo(f.OwnerDid(), f.Knot, f.DidSlashRepo())
	if err != nil {
		rp.pages.Notice(w, noticeId, "Failed to update RBAC rules")
		return
	}

	// remove repo from db
	err = db.RemoveRepo(tx, f.OwnerDid(), f.Name)
	if err != nil {
		rp.pages.Notice(w, noticeId, "Failed to update appview")
		return
	}
	l.Info("removed repo from db")

	err = tx.Commit()
	if err != nil {
		l.Error("failed to commit changes", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = rp.enforcer.E.SavePolicy()
	if err != nil {
		l.Error("failed to update ACLs", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rp.pages.HxRedirect(w, fmt.Sprintf("/%s", f.OwnerDid()))
}

func (rp *Repo) SetDefaultBranch(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "SetDefaultBranch")

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	noticeId := "operation-error"
	branch := r.FormValue("branch")
	if branch == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	client, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoSetDefaultBranchNSID),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		l.Error("failed to connect to knot server", "err", err)
		rp.pages.Notice(w, noticeId, "Failed to connect to knot server.")
		return
	}

	xe := tangled.RepoSetDefaultBranch(
		r.Context(),
		client,
		&tangled.RepoSetDefaultBranch_Input{
			Repo:          f.RepoAt().String(),
			DefaultBranch: branch,
		},
	)
	if err := xrpcclient.HandleXrpcErr(xe); err != nil {
		l.Error("xrpc failed", "err", xe)
		rp.pages.Notice(w, noticeId, err.Error())
		return
	}

	rp.pages.HxRefresh(w)
}

func (rp *Repo) Secrets(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "Secrets")
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	if f.Spindle == "" {
		l.Error("empty spindle cannot add/rm secret", "err", err)
		return
	}

	lxm := tangled.RepoAddSecretNSID
	if r.Method == http.MethodDelete {
		lxm = tangled.RepoRemoveSecretNSID
	}

	spindleClient, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Spindle),
		oauth.WithLxm(lxm),
		oauth.WithExp(60),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		l.Error("failed to create spindle client", "err", err)
		return
	}

	key := r.FormValue("key")
	if key == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		errorId := "add-secret-error"

		value := r.FormValue("value")
		if value == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		err = tangled.RepoAddSecret(
			r.Context(),
			spindleClient,
			&tangled.RepoAddSecret_Input{
				Repo:  f.RepoAt().String(),
				Key:   key,
				Value: value,
			},
		)
		if err != nil {
			l.Error("Failed to add secret.", "err", err)
			rp.pages.Notice(w, errorId, "Failed to add secret.")
			return
		}

	case http.MethodDelete:
		errorId := "operation-error"

		err = tangled.RepoRemoveSecret(
			r.Context(),
			spindleClient,
			&tangled.RepoRemoveSecret_Input{
				Repo: f.RepoAt().String(),
				Key:  key,
			},
		)
		if err != nil {
			l.Error("Failed to delete secret.", "err", err)
			rp.pages.Notice(w, errorId, "Failed to delete secret.")
			return
		}
	}

	rp.pages.HxRefresh(w)
}

type tab = map[string]any

var (
	// would be great to have ordered maps right about now
	settingsTabs []tab = []tab{
		{"Name": "general", "Icon": "sliders-horizontal"},
		{"Name": "access", "Icon": "users"},
		{"Name": "pipelines", "Icon": "layers-2"},
	}
)

func (rp *Repo) RepoSettings(w http.ResponseWriter, r *http.Request) {
	tabVal := r.URL.Query().Get("tab")
	if tabVal == "" {
		tabVal = "general"
	}

	switch tabVal {
	case "general":
		rp.generalSettings(w, r)

	case "access":
		rp.accessSettings(w, r)

	case "pipelines":
		rp.pipelineSettings(w, r)
	}
}

func (rp *Repo) generalSettings(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "generalSettings")

	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	xrpcBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoBranchesResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		l.Error("failed to decode XRPC response", "err", err)
		rp.pages.Error503(w)
		return
	}

	defaultLabels, err := db.GetLabelDefinitions(rp.db, db.FilterIn("at_uri", models.DefaultLabelDefs()))
	if err != nil {
		l.Error("failed to fetch labels", "err", err)
		rp.pages.Error503(w)
		return
	}

	labels, err := db.GetLabelDefinitions(rp.db, db.FilterIn("at_uri", f.Repo.Labels))
	if err != nil {
		l.Error("failed to fetch labels", "err", err)
		rp.pages.Error503(w)
		return
	}
	// remove default labels from the labels list, if present
	defaultLabelMap := make(map[string]bool)
	for _, dl := range defaultLabels {
		defaultLabelMap[dl.AtUri().String()] = true
	}
	n := 0
	for _, l := range labels {
		if !defaultLabelMap[l.AtUri().String()] {
			labels[n] = l
			n++
		}
	}
	labels = labels[:n]

	subscribedLabels := make(map[string]struct{})
	for _, l := range f.Repo.Labels {
		subscribedLabels[l] = struct{}{}
	}

	// if there is atleast 1 unsubbed default label, show the "subscribe all" button,
	// if all default labels are subbed, show the "unsubscribe all" button
	shouldSubscribeAll := false
	for _, dl := range defaultLabels {
		if _, ok := subscribedLabels[dl.AtUri().String()]; !ok {
			// one of the default labels is not subscribed to
			shouldSubscribeAll = true
			break
		}
	}

	rp.pages.RepoGeneralSettings(w, pages.RepoGeneralSettingsParams{
		LoggedInUser:       user,
		RepoInfo:           f.RepoInfo(user),
		Branches:           result.Branches,
		Labels:             labels,
		DefaultLabels:      defaultLabels,
		SubscribedLabels:   subscribedLabels,
		ShouldSubscribeAll: shouldSubscribeAll,
		Tabs:               settingsTabs,
		Tab:                "general",
	})
}

func (rp *Repo) accessSettings(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "accessSettings")

	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	repoCollaborators, err := f.Collaborators(r.Context())
	if err != nil {
		l.Error("failed to get collaborators", "err", err)
	}

	rp.pages.RepoAccessSettings(w, pages.RepoAccessSettingsParams{
		LoggedInUser:  user,
		RepoInfo:      f.RepoInfo(user),
		Tabs:          settingsTabs,
		Tab:           "access",
		Collaborators: repoCollaborators,
	})
}

func (rp *Repo) pipelineSettings(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "pipelineSettings")

	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	// all spindles that the repo owner is a member of
	spindles, err := rp.enforcer.GetSpindlesForUser(f.OwnerDid())
	if err != nil {
		l.Error("failed to fetch spindles", "err", err)
		return
	}

	var secrets []*tangled.RepoListSecrets_Secret
	if f.Spindle != "" {
		if spindleClient, err := rp.oauth.ServiceClient(
			r,
			oauth.WithService(f.Spindle),
			oauth.WithLxm(tangled.RepoListSecretsNSID),
			oauth.WithExp(60),
			oauth.WithDev(rp.config.Core.Dev),
		); err != nil {
			l.Error("failed to create spindle client", "err", err)
		} else if resp, err := tangled.RepoListSecrets(r.Context(), spindleClient, f.RepoAt().String()); err != nil {
			l.Error("failed to fetch secrets", "err", err)
		} else {
			secrets = resp.Secrets
		}
	}

	slices.SortFunc(secrets, func(a, b *tangled.RepoListSecrets_Secret) int {
		return strings.Compare(a.Key, b.Key)
	})

	var dids []string
	for _, s := range secrets {
		dids = append(dids, s.CreatedBy)
	}
	resolvedIdents := rp.idResolver.ResolveIdents(r.Context(), dids)

	// convert to a more manageable form
	var niceSecret []map[string]any
	for id, s := range secrets {
		when, _ := time.Parse(time.RFC3339, s.CreatedAt)
		niceSecret = append(niceSecret, map[string]any{
			"Id":        id,
			"Key":       s.Key,
			"CreatedAt": when,
			"CreatedBy": resolvedIdents[id].Handle.String(),
		})
	}

	rp.pages.RepoPipelineSettings(w, pages.RepoPipelineSettingsParams{
		LoggedInUser:   user,
		RepoInfo:       f.RepoInfo(user),
		Tabs:           settingsTabs,
		Tab:            "pipelines",
		Spindles:       spindles,
		CurrentSpindle: f.Spindle,
		Secrets:        niceSecret,
	})
}

func (rp *Repo) SyncRepoFork(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "SyncRepoFork")

	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to resolve source repo", "err", err)
		return
	}

	switch r.Method {
	case http.MethodPost:
		client, err := rp.oauth.ServiceClient(
			r,
			oauth.WithService(f.Knot),
			oauth.WithLxm(tangled.RepoForkSyncNSID),
			oauth.WithDev(rp.config.Core.Dev),
		)
		if err != nil {
			rp.pages.Notice(w, "repo", "Failed to connect to knot server.")
			return
		}

		repoInfo := f.RepoInfo(user)
		if repoInfo.Source == nil {
			rp.pages.Notice(w, "repo", "This repository is not a fork.")
			return
		}

		err = tangled.RepoForkSync(
			r.Context(),
			client,
			&tangled.RepoForkSync_Input{
				Did:    user.Did,
				Name:   f.Name,
				Source: repoInfo.Source.RepoAt().String(),
				Branch: ref,
			},
		)
		if err := xrpcclient.HandleXrpcErr(err); err != nil {
			rp.pages.Notice(w, "repo", err.Error())
			return
		}

		rp.pages.HxRefresh(w)
		return
	}
}

func (rp *Repo) ForkRepo(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "ForkRepo")

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to resolve source repo", "err", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		user := rp.oauth.GetUser(r)
		knots, err := rp.enforcer.GetKnotsForUser(user.Did)
		if err != nil {
			rp.pages.Notice(w, "repo", "Invalid user account.")
			return
		}

		rp.pages.ForkRepo(w, pages.ForkRepoParams{
			LoggedInUser: user,
			Knots:        knots,
			RepoInfo:     f.RepoInfo(user),
		})

	case http.MethodPost:
		l := rp.logger.With("handler", "ForkRepo")

		targetKnot := r.FormValue("knot")
		if targetKnot == "" {
			rp.pages.Notice(w, "repo", "Invalid form submission&mdash;missing knot domain.")
			return
		}
		l = l.With("targetKnot", targetKnot)

		ok, err := rp.enforcer.E.Enforce(user.Did, targetKnot, targetKnot, "repo:create")
		if err != nil || !ok {
			rp.pages.Notice(w, "repo", "You do not have permission to create a repo in this knot.")
			return
		}

		// choose a name for a fork
		forkName := r.FormValue("repo_name")
		if forkName == "" {
			rp.pages.Notice(w, "repo", "Repository name cannot be empty.")
			return
		}

		// this check is *only* to see if the forked repo name already exists
		// in the user's account.
		existingRepo, err := db.GetRepo(
			rp.db,
			db.FilterEq("did", user.Did),
			db.FilterEq("name", forkName),
		)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				l.Error("error fetching existing repo from db", "err", err)
				rp.pages.Notice(w, "repo", "Failed to fork this repository. Try again later.")
				return
			}
		} else if existingRepo != nil {
			// repo with this name already exists
			rp.pages.Notice(w, "repo", "A repository with this name already exists.")
			return
		}
		l = l.With("forkName", forkName)

		uri := "https"
		if rp.config.Core.Dev {
			uri = "http"
		}

		forkSourceUrl := fmt.Sprintf("%s://%s/%s/%s", uri, f.Knot, f.OwnerDid(), f.Repo.Name)
		l = l.With("cloneUrl", forkSourceUrl)

		sourceAt := f.RepoAt().String()

		// create an atproto record for this fork
		rkey := tid.TID()
		repo := &models.Repo{
			Did:         user.Did,
			Name:        forkName,
			Knot:        targetKnot,
			Rkey:        rkey,
			Source:      sourceAt,
			Description: f.Repo.Description,
			Created:     time.Now(),
			Labels:      models.DefaultLabelDefs(),
		}
		record := repo.AsRecord()

		atpClient, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to create xrpcclient", "err", err)
			rp.pages.Notice(w, "repo", "Failed to fork repository.")
			return
		}

		atresp, err := comatproto.RepoPutRecord(r.Context(), atpClient, &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &record,
			},
		})
		if err != nil {
			l.Error("failed to write to PDS", "err", err)
			rp.pages.Notice(w, "repo", "Failed to announce repository creation.")
			return
		}

		aturi := atresp.Uri
		l = l.With("aturi", aturi)
		l.Info("wrote to PDS")

		tx, err := rp.db.BeginTx(r.Context(), nil)
		if err != nil {
			l.Info("txn failed", "err", err)
			rp.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// The rollback function reverts a few things on failure:
		// - the pending txn
		// - the ACLs
		// - the atproto record created
		rollback := func() {
			err1 := tx.Rollback()
			err2 := rp.enforcer.E.LoadPolicy()
			err3 := rollbackRecord(context.Background(), aturi, atpClient)

			// ignore txn complete errors, this is okay
			if errors.Is(err1, sql.ErrTxDone) {
				err1 = nil
			}

			if errs := errors.Join(err1, err2, err3); errs != nil {
				l.Error("failed to rollback changes", "errs", errs)
				return
			}
		}
		defer rollback()

		client, err := rp.oauth.ServiceClient(
			r,
			oauth.WithService(targetKnot),
			oauth.WithLxm(tangled.RepoCreateNSID),
			oauth.WithDev(rp.config.Core.Dev),
		)
		if err != nil {
			l.Error("could not create service client", "err", err)
			rp.pages.Notice(w, "repo", "Failed to connect to knot server.")
			return
		}

		err = tangled.RepoCreate(
			r.Context(),
			client,
			&tangled.RepoCreate_Input{
				Rkey:   rkey,
				Source: &forkSourceUrl,
			},
		)
		if err := xrpcclient.HandleXrpcErr(err); err != nil {
			rp.pages.Notice(w, "repo", err.Error())
			return
		}

		err = db.AddRepo(tx, repo)
		if err != nil {
			l.Error("failed to AddRepo", "err", err)
			rp.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// acls
		p, _ := securejoin.SecureJoin(user.Did, forkName)
		err = rp.enforcer.AddRepo(user.Did, targetKnot, p)
		if err != nil {
			l.Error("failed to add ACLs", "err", err)
			rp.pages.Notice(w, "repo", "Failed to set up repository permissions.")
			return
		}

		err = tx.Commit()
		if err != nil {
			l.Error("failed to commit changes", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = rp.enforcer.E.SavePolicy()
		if err != nil {
			l.Error("failed to update ACLs", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// reset the ATURI because the transaction completed successfully
		aturi = ""

		rp.notifier.NewRepo(r.Context(), repo)
		rp.pages.HxLocation(w, fmt.Sprintf("/@%s/%s", user.Did, forkName))
	}
}

// this is used to rollback changes made to the PDS
//
// it is a no-op if the provided ATURI is empty
func rollbackRecord(ctx context.Context, aturi string, client *atpclient.APIClient) error {
	if aturi == "" {
		return nil
	}

	parsed := syntax.ATURI(aturi)

	collection := parsed.Collection().String()
	repo := parsed.Authority().String()
	rkey := parsed.RecordKey().String()

	_, err := comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
		Collection: collection,
		Repo:       repo,
		Rkey:       rkey,
	})
	return err
}

func (rp *Repo) RepoCompareNew(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoCompareNew")

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)
	branchBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var branchResult types.RepoBranchesResponse
	if err := json.Unmarshal(branchBytes, &branchResult); err != nil {
		l.Error("failed to decode XRPC branches response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}
	branches := branchResult.Branches

	sortBranches(branches)

	var defaultBranch string
	for _, b := range branches {
		if b.IsDefault {
			defaultBranch = b.Name
		}
	}

	base := defaultBranch
	head := defaultBranch

	params := r.URL.Query()
	queryBase := params.Get("base")
	queryHead := params.Get("head")
	if queryBase != "" {
		base = queryBase
	}
	if queryHead != "" {
		head = queryHead
	}

	tagBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tags", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var tags types.RepoTagsResponse
	if err := json.Unmarshal(tagBytes, &tags); err != nil {
		l.Error("failed to decode XRPC tags response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	repoinfo := f.RepoInfo(user)

	rp.pages.RepoCompareNew(w, pages.RepoCompareNewParams{
		LoggedInUser: user,
		RepoInfo:     repoinfo,
		Branches:     branches,
		Tags:         tags.Tags,
		Base:         base,
		Head:         head,
	})
}

func (rp *Repo) RepoCompare(w http.ResponseWriter, r *http.Request) {
	l := rp.logger.With("handler", "RepoCompare")

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		l.Error("failed to get repo and knot", "err", err)
		return
	}

	var diffOpts types.DiffOpts
	if d := r.URL.Query().Get("diff"); d == "split" {
		diffOpts.Split = true
	}

	// if user is navigating to one of
	//   /compare/{base}/{head}
	//   /compare/{base}...{head}
	base := chi.URLParam(r, "base")
	head := chi.URLParam(r, "head")
	if base == "" && head == "" {
		rest := chi.URLParam(r, "*") // master...feature/xyz
		parts := strings.SplitN(rest, "...", 2)
		if len(parts) == 2 {
			base = parts[0]
			head = parts[1]
		}
	}

	base, _ = url.PathUnescape(base)
	head, _ = url.PathUnescape(head)

	if base == "" || head == "" {
		l.Error("invalid comparison")
		rp.pages.Error404(w)
		return
	}

	scheme := "http"
	if !rp.config.Core.Dev {
		scheme = "https"
	}
	host := fmt.Sprintf("%s://%s", scheme, f.Knot)
	xrpcc := &indigoxrpc.Client{
		Host: host,
	}

	repo := fmt.Sprintf("%s/%s", f.OwnerDid(), f.Name)

	branchBytes, err := tangled.RepoBranches(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.branches", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var branches types.RepoBranchesResponse
	if err := json.Unmarshal(branchBytes, &branches); err != nil {
		l.Error("failed to decode XRPC branches response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	tagBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.tags", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var tags types.RepoTagsResponse
	if err := json.Unmarshal(tagBytes, &tags); err != nil {
		l.Error("failed to decode XRPC tags response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	compareBytes, err := tangled.RepoCompare(r.Context(), xrpcc, repo, base, head)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		l.Error("failed to call XRPC repo.compare", "err", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var formatPatch types.RepoFormatPatchResponse
	if err := json.Unmarshal(compareBytes, &formatPatch); err != nil {
		l.Error("failed to decode XRPC compare response", "err", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	var diff types.NiceDiff
	if formatPatch.CombinedPatchRaw != "" {
		diff = patchutil.AsNiceDiff(formatPatch.CombinedPatchRaw, base)
	} else {
		diff = patchutil.AsNiceDiff(formatPatch.FormatPatchRaw, base)
	}

	repoinfo := f.RepoInfo(user)

	rp.pages.RepoCompare(w, pages.RepoCompareParams{
		LoggedInUser: user,
		RepoInfo:     repoinfo,
		Branches:     branches.Branches,
		Tags:         tags.Tags,
		Base:         base,
		Head:         head,
		Diff:         &diff,
		DiffOpts:     diffOpts,
	})

}
