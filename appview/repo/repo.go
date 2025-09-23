package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	indigoxrpc "github.com/bluesky-social/indigo/xrpc"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/commitverify"
	"tangled.org/core/appview/config"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/notify"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/appview/reporesolver"
	xrpcclient "tangled.org/core/appview/xrpcclient"
	"tangled.org/core/eventconsumer"
	"tangled.org/core/idresolver"
	"tangled.org/core/patchutil"
	"tangled.org/core/rbac"
	"tangled.org/core/tid"
	"tangled.org/core/types"
	"tangled.org/core/xrpc/serviceauth"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/bluesky-social/indigo/atproto/syntax"
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
	}
}

func (rp *Repo) DownloadArchive(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
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
		log.Println("failed to call XRPC repo.archive", xrpcerr)
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
	f, err := rp.repoResolver.Resolve(r)
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
		log.Println("failed to call XRPC repo.log", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var xrpcResp types.RepoLogResponse
	if err := json.Unmarshal(xrpcBytes, &xrpcResp); err != nil {
		log.Println("failed to decode XRPC response", err)
		rp.pages.Error503(w)
		return
	}

	tagBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		log.Println("failed to call XRPC repo.tags", xrpcerr)
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
		log.Println("failed to call XRPC repo.branches", xrpcerr)
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
		log.Println("failed to fetch email to did mapping", err)
	}

	vc, err := commitverify.GetVerifiedObjectCommits(rp.db, emailToDidMap, xrpcResp.Commits)
	if err != nil {
		log.Println(err)
	}

	repoInfo := f.RepoInfo(user)

	var shas []string
	for _, c := range xrpcResp.Commits {
		shas = append(shas, c.Hash.String())
	}
	pipelines, err := getPipelineStatuses(rp.db, repoInfo, shas)
	if err != nil {
		log.Println(err)
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
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	user := rp.oauth.GetUser(r)
	rp.pages.EditRepoDescriptionFragment(w, pages.RepoDescriptionParams{
		RepoInfo: f.RepoInfo(user),
	})
}

func (rp *Repo) RepoDescription(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	repoAt := f.RepoAt()
	rkey := repoAt.RecordKey().String()
	if rkey == "" {
		log.Println("invalid aturi for repo", err)
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
			log.Println("failed to get client")
			rp.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// optimistic update
		err = db.UpdateDescription(rp.db, string(repoAt), newDescription)
		if err != nil {
			log.Println("failed to perferom update-description query", err)
			rp.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// this is a bit of a pain because the golang atproto impl does not allow nil SwapRecord field
		//
		// SwapRecord is optional and should happen automagically, but given that it does not, we have to perform two requests
		ex, err := client.RepoGetRecord(r.Context(), "", tangled.RepoNSID, user.Did, rkey)
		if err != nil {
			// failed to get record
			rp.pages.Notice(w, "repo-notice", "Failed to update description, no record found on PDS.")
			return
		}
		_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.RepoNSID,
			Repo:       user.Did,
			Rkey:       rkey,
			SwapRecord: ex.Cid,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.Repo{
					Knot:        f.Knot,
					Name:        f.Name,
					Owner:       user.Did,
					CreatedAt:   f.Created.Format(time.RFC3339),
					Description: &newDescription,
					Spindle:     &f.Spindle,
				},
			},
		})

		if err != nil {
			log.Println("failed to perferom update-description query", err)
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
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
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
		log.Println("failed to call XRPC repo.diff", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoCommitResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		log.Println("failed to decode XRPC response", err)
		rp.pages.Error503(w)
		return
	}

	emailToDidMap, err := db.GetEmailToDid(rp.db, []string{result.Diff.Commit.Committer.Email, result.Diff.Commit.Author.Email}, true)
	if err != nil {
		log.Println("failed to get email to did mapping:", err)
	}

	vc, err := commitverify.GetVerifiedCommits(rp.db, emailToDidMap, []types.NiceDiff{*result.Diff})
	if err != nil {
		log.Println(err)
	}

	user := rp.oauth.GetUser(r)
	repoInfo := f.RepoInfo(user)
	pipelines, err := getPipelineStatuses(rp.db, repoInfo, []string{result.Diff.Commit.This})
	if err != nil {
		log.Println(err)
		// non-fatal
	}
	var pipeline *db.Pipeline
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
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
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
		log.Println("failed to call XRPC repo.tree", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	// readme content
	var (
		readmeContent  string
		readmeFileName string
	)

	for _, filename := range markup.ReadmeFilenames {
		path := fmt.Sprintf("%s/%s", treePath, filename)
		blobResp, err := tangled.RepoBlob(r.Context(), xrpcc, path, false, ref, repo)
		if err != nil {
			continue
		}

		if blobResp == nil {
			continue
		}

		readmeContent = blobResp.Content
		readmeFileName = path
		break
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
		Readme:           readmeContent,
		ReadmeFileName:   readmeFileName,
		RepoTreeResponse: result,
	})
}

func (rp *Repo) RepoTags(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
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
		log.Println("failed to call XRPC repo.tags", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoTagsResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		log.Println("failed to decode XRPC response", err)
		rp.pages.Error503(w)
		return
	}

	artifacts, err := db.GetArtifact(rp.db, db.FilterEq("repo_at", f.RepoAt()))
	if err != nil {
		log.Println("failed grab artifacts", err)
		return
	}

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
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
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
		log.Println("failed to call XRPC repo.branches", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoBranchesResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		log.Println("failed to decode XRPC response", err)
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

func (rp *Repo) RepoBlob(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
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
		log.Println("failed to call XRPC repo.blob", xrpcerr)
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
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
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
		log.Println("failed to create request", err)
		return
	}

	// forward the If-None-Match header
	if clientETag := r.Header.Get("If-None-Match"); clientETag != "" {
		req.Header.Set("If-None-Match", clientETag)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Println("failed to reach knotserver", err)
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
		log.Printf("knotserver returned non-OK status for raw blob %s: %d", blobURL, resp.StatusCode)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body from knotserver: %v", err)
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
	l = l.With("handle", user.Handle)

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

	repoAt := f.RepoAt()
	rkey := repoAt.RecordKey().String()
	if rkey == "" {
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

	spindlePtr := &newSpindle
	if removingSpindle {
		spindlePtr = nil
	}

	// optimistic update
	err = db.UpdateSpindle(rp.db, string(repoAt), spindlePtr)
	if err != nil {
		fail("Failed to update spindle. Try again later.", err)
		return
	}

	ex, err := client.RepoGetRecord(r.Context(), "", tangled.RepoNSID, user.Did, rkey)
	if err != nil {
		fail("Failed to update spindle, no record found on PDS.", err)
		return
	}
	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       user.Did,
		Rkey:       rkey,
		SwapRecord: ex.Cid,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.Repo{
				Knot:        f.Knot,
				Name:        f.Name,
				Owner:       user.Did,
				CreatedAt:   f.Created.Format(time.RFC3339),
				Description: &f.Description,
				Spindle:     spindlePtr,
			},
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

func (rp *Repo) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "AddCollaborator")
	l = l.With("did", user.Did)
	l = l.With("handle", user.Handle)

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
	resp, err := client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
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

	err = db.AddCollaborator(rp.db, db.Collaborator{
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

	noticeId := "operation-error"
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	// remove record from pds
	xrpcClient, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to get authorized client", err)
		return
	}
	_, err = xrpcClient.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       user.Did,
		Rkey:       f.Rkey,
	})
	if err != nil {
		log.Printf("failed to delete record: %s", err)
		rp.pages.Notice(w, noticeId, "Failed to delete repository from PDS.")
		return
	}
	log.Println("removed repo record ", f.RepoAt().String())

	client, err := rp.oauth.ServiceClient(
		r,
		oauth.WithService(f.Knot),
		oauth.WithLxm(tangled.RepoDeleteNSID),
		oauth.WithDev(rp.config.Core.Dev),
	)
	if err != nil {
		log.Println("failed to connect to knot server:", err)
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
	log.Println("deleted repo from knot")

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start tx")
		w.Write(fmt.Append(nil, "failed to add collaborator: ", err))
		return
	}
	defer func() {
		tx.Rollback()
		err = rp.enforcer.E.LoadPolicy()
		if err != nil {
			log.Println("failed to rollback policies")
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
	log.Println("removed collaborators")

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
	log.Println("removed repo from db")

	err = tx.Commit()
	if err != nil {
		log.Println("failed to commit changes", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = rp.enforcer.E.SavePolicy()
	if err != nil {
		log.Println("failed to update ACLs", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rp.pages.HxRedirect(w, fmt.Sprintf("/%s", f.OwnerDid()))
}

func (rp *Repo) SetDefaultBranch(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
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
		log.Println("failed to connect to knot server:", err)
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
		log.Println("xrpc failed", "err", xe)
		rp.pages.Notice(w, noticeId, err.Error())
		return
	}

	rp.pages.HxRefresh(w)
}

func (rp *Repo) Secrets(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	l := rp.logger.With("handler", "Secrets")
	l = l.With("handle", user.Handle)
	l = l.With("did", user.Did)

	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	if f.Spindle == "" {
		log.Println("empty spindle cannot add/rm secret", err)
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
		log.Println("failed to create spindle client", err)
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
		log.Println("failed to call XRPC repo.branches", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var result types.RepoBranchesResponse
	if err := json.Unmarshal(xrpcBytes, &result); err != nil {
		log.Println("failed to decode XRPC response", err)
		rp.pages.Error503(w)
		return
	}

	rp.pages.RepoGeneralSettings(w, pages.RepoGeneralSettingsParams{
		LoggedInUser: user,
		RepoInfo:     f.RepoInfo(user),
		Branches:     result.Branches,
		Tabs:         settingsTabs,
		Tab:          "general",
	})
}

func (rp *Repo) accessSettings(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	repoCollaborators, err := f.Collaborators(r.Context())
	if err != nil {
		log.Println("failed to get collaborators", err)
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
	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	// all spindles that the repo owner is a member of
	spindles, err := rp.enforcer.GetSpindlesForUser(f.OwnerDid())
	if err != nil {
		log.Println("failed to fetch spindles", err)
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
			log.Println("failed to create spindle client", err)
		} else if resp, err := tangled.RepoListSecrets(r.Context(), spindleClient, f.RepoAt().String()); err != nil {
			log.Println("failed to fetch secrets", err)
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
	ref := chi.URLParam(r, "ref")
	ref, _ = url.PathUnescape(ref)

	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Printf("failed to resolve source repo: %v", err)
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
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Printf("failed to resolve source repo: %v", err)
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
		forkName := f.Name
		// this check is *only* to see if the forked repo name already exists
		// in the user's account.
		existingRepo, err := db.GetRepo(rp.db, user.Did, f.Name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// no existing repo with this name found, we can use the name as is
			} else {
				log.Println("error fetching existing repo from db", err)
				rp.pages.Notice(w, "repo", "Failed to fork this repository. Try again later.")
				return
			}
		} else if existingRepo != nil {
			// repo with this name already exists, append random string
			forkName = fmt.Sprintf("%s-%s", forkName, randomString(3))
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
		repo := &db.Repo{
			Did:    user.Did,
			Name:   forkName,
			Knot:   targetKnot,
			Rkey:   rkey,
			Source: sourceAt,
		}

		xrpcClient, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			l.Error("failed to create xrpcclient", "err", err)
			rp.pages.Notice(w, "repo", "Failed to fork repository.")
			return
		}

		createdAt := time.Now().Format(time.RFC3339)
		atresp, err := xrpcClient.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
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
			err3 := rollbackRecord(context.Background(), aturi, xrpcClient)

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
			log.Println(err)
			rp.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// acls
		p, _ := securejoin.SecureJoin(user.Did, forkName)
		err = rp.enforcer.AddRepo(user.Did, targetKnot, p)
		if err != nil {
			log.Println(err)
			rp.pages.Notice(w, "repo", "Failed to set up repository permissions.")
			return
		}

		err = tx.Commit()
		if err != nil {
			log.Println("failed to commit changes", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = rp.enforcer.E.SavePolicy()
		if err != nil {
			log.Println("failed to update ACLs", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// reset the ATURI because the transaction completed successfully
		aturi = ""

		rp.notifier.NewRepo(r.Context(), repo)
		rp.pages.HxLocation(w, fmt.Sprintf("/@%s/%s", user.Handle, forkName))
	}
}

// this is used to rollback changes made to the PDS
//
// it is a no-op if the provided ATURI is empty
func rollbackRecord(ctx context.Context, aturi string, xrpcc *xrpcclient.Client) error {
	if aturi == "" {
		return nil
	}

	parsed := syntax.ATURI(aturi)

	collection := parsed.Collection().String()
	repo := parsed.Authority().String()
	rkey := parsed.RecordKey().String()

	_, err := xrpcc.RepoDeleteRecord(ctx, &comatproto.RepoDeleteRecord_Input{
		Collection: collection,
		Repo:       repo,
		Rkey:       rkey,
	})
	return err
}

func (rp *Repo) RepoCompareNew(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
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
		log.Println("failed to call XRPC repo.branches", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var branchResult types.RepoBranchesResponse
	if err := json.Unmarshal(branchBytes, &branchResult); err != nil {
		log.Println("failed to decode XRPC branches response", err)
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
		log.Println("failed to call XRPC repo.tags", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var tags types.RepoTagsResponse
	if err := json.Unmarshal(tagBytes, &tags); err != nil {
		log.Println("failed to decode XRPC tags response", err)
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
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
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
		log.Printf("invalid comparison")
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
		log.Println("failed to call XRPC repo.branches", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var branches types.RepoBranchesResponse
	if err := json.Unmarshal(branchBytes, &branches); err != nil {
		log.Println("failed to decode XRPC branches response", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	tagBytes, err := tangled.RepoTags(r.Context(), xrpcc, "", 0, repo)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		log.Println("failed to call XRPC repo.tags", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var tags types.RepoTagsResponse
	if err := json.Unmarshal(tagBytes, &tags); err != nil {
		log.Println("failed to decode XRPC tags response", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	compareBytes, err := tangled.RepoCompare(r.Context(), xrpcc, repo, base, head)
	if xrpcerr := xrpcclient.HandleXrpcErr(err); xrpcerr != nil {
		log.Println("failed to call XRPC repo.compare", xrpcerr)
		rp.pages.Error503(w)
		return
	}

	var formatPatch types.RepoFormatPatchResponse
	if err := json.Unmarshal(compareBytes, &formatPatch); err != nil {
		log.Println("failed to decode XRPC compare response", err)
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		return
	}

	diff := patchutil.AsNiceDiff(formatPatch.Patch, base)

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
