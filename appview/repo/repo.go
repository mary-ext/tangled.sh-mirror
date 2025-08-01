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

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/commitverify"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/notify"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/tid"
	"tangled.sh/tangled.sh/core/types"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
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
	refParam := chi.URLParam(r, "ref")
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	var uri string
	if rp.config.Core.Dev {
		uri = "http"
	} else {
		uri = "https"
	}
	url := fmt.Sprintf("%s://%s/%s/%s/archive/%s.tar.gz", uri, f.Knot, f.OwnerDid(), f.Name, url.PathEscape(refParam))

	http.Redirect(w, r, url, http.StatusFound)
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

	us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
	if err != nil {
		log.Println("failed to create unsigned client", err)
		return
	}

	repolog, err := us.Log(f.OwnerDid(), f.Name, ref, page)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	tagResult, err := us.Tags(f.OwnerDid(), f.Name)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	tagMap := make(map[string][]string)
	for _, tag := range tagResult.Tags {
		hash := tag.Hash
		if tag.Tag != nil {
			hash = tag.Tag.Target.String()
		}
		tagMap[hash] = append(tagMap[hash], tag.Name)
	}

	branchResult, err := us.Branches(f.OwnerDid(), f.Name)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	for _, branch := range branchResult.Branches {
		hash := branch.Hash
		tagMap[hash] = append(tagMap[hash], branch.Name)
	}

	user := rp.oauth.GetUser(r)

	emailToDidMap, err := db.GetEmailToDid(rp.db, uniqueEmails(repolog.Commits), true)
	if err != nil {
		log.Println("failed to fetch email to did mapping", err)
	}

	vc, err := commitverify.GetVerifiedObjectCommits(rp.db, emailToDidMap, repolog.Commits)
	if err != nil {
		log.Println(err)
	}

	repoInfo := f.RepoInfo(user)

	var shas []string
	for _, c := range repolog.Commits {
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
		RepoLogResponse:    *repolog,
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
	protocol := "http"
	if !rp.config.Core.Dev {
		protocol = "https"
	}

	var diffOpts types.DiffOpts
	if d := r.URL.Query().Get("diff"); d == "split" {
		diffOpts.Split = true
	}

	if !plumbing.IsHash(ref) {
		rp.pages.Error404(w)
		return
	}

	resp, err := http.Get(fmt.Sprintf("%s://%s/%s/%s/commit/%s", protocol, f.Knot, f.OwnerDid(), f.Repo.Name, ref))
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
	treePath := chi.URLParam(r, "*")
	protocol := "http"
	if !rp.config.Core.Dev {
		protocol = "https"
	}
	resp, err := http.Get(fmt.Sprintf("%s://%s/%s/%s/tree/%s/%s", protocol, f.Knot, f.OwnerDid(), f.Repo.Name, ref, treePath))
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

	// redirects tree paths trying to access a blob; in this case the result.Files is unpopulated,
	// so we can safely redirect to the "parent" (which is the same file).
	unescapedTreePath, _ := url.PathUnescape(treePath)
	if len(result.Files) == 0 && result.Parent == unescapedTreePath {
		http.Redirect(w, r, fmt.Sprintf("/%s/blob/%s/%s", f.OwnerSlashRepo(), ref, result.Parent), http.StatusFound)
		return
	}

	user := rp.oauth.GetUser(r)

	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.Name, fmt.Sprintf("/%s/tree/%s", f.OwnerSlashRepo(), ref)})
	if treePath != "" {
		for idx, elem := range strings.Split(treePath, "/") {
			breadcrumbs = append(breadcrumbs, []string{elem, fmt.Sprintf("%s/%s", breadcrumbs[idx][1], elem)})
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
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
	if err != nil {
		log.Println("failed to create unsigned client", err)
		return
	}

	result, err := us.Tags(f.OwnerDid(), f.Name)
	if err != nil {
		log.Println("failed to reach knotserver", err)
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
		RepoTagsResponse:  *result,
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

	us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
	if err != nil {
		log.Println("failed to create unsigned client", err)
		return
	}

	result, err := us.Branches(f.OwnerDid(), f.Name)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	sortBranches(result.Branches)

	user := rp.oauth.GetUser(r)
	rp.pages.RepoBranches(w, pages.RepoBranchesParams{
		LoggedInUser:         user,
		RepoInfo:             f.RepoInfo(user),
		RepoBranchesResponse: *result,
	})
}

func (rp *Repo) RepoBlob(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	filePath := chi.URLParam(r, "*")
	protocol := "http"
	if !rp.config.Core.Dev {
		protocol = "https"
	}
	resp, err := http.Get(fmt.Sprintf("%s://%s/%s/%s/blob/%s/%s", protocol, f.Knot, f.OwnerDid(), f.Repo.Name, ref, filePath))
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
	breadcrumbs = append(breadcrumbs, []string{f.Name, fmt.Sprintf("/%s/tree/%s", f.OwnerSlashRepo(), ref)})
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

	var unsupported bool
	var isImage bool
	var isVideo bool
	var contentSrc string

	if result.IsBinary {
		ext := strings.ToLower(filepath.Ext(result.Path))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp":
			isImage = true
		case ".mp4", ".webm", ".ogg", ".mov", ".avi":
			isVideo = true
		default:
			unsupported = true
		}

		// fetch the actual binary content like in RepoBlobRaw

		blobURL := fmt.Sprintf("%s://%s/%s/%s/raw/%s/%s", protocol, f.Knot, f.OwnerDid(), f.Name, ref, filePath)
		contentSrc = blobURL
		if !rp.config.Core.Dev {
			contentSrc = markup.GenerateCamoURL(rp.config.Camo.Host, rp.config.Camo.SharedSecret, blobURL)
		}
	}

	user := rp.oauth.GetUser(r)
	rp.pages.RepoBlob(w, pages.RepoBlobParams{
		LoggedInUser:     user,
		RepoInfo:         f.RepoInfo(user),
		RepoBlobResponse: result,
		BreadCrumbs:      breadcrumbs,
		ShowRendered:     showRendered,
		RenderToggle:     renderToggle,
		Unsupported:      unsupported,
		IsImage:          isImage,
		IsVideo:          isVideo,
		ContentSrc:       contentSrc,
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
	filePath := chi.URLParam(r, "*")

	protocol := "http"
	if !rp.config.Core.Dev {
		protocol = "https"
	}
	blobURL := fmt.Sprintf("%s://%s/%s/%s/raw/%s/%s", protocol, f.Knot, f.OwnerDid(), f.Repo.Name, ref, filePath)
	resp, err := http.Get(blobURL)
	if err != nil {
		log.Println("failed to reach knotserver:", err)
		rp.pages.Error503(w)
		return
	}
	defer resp.Body.Close()

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

	if strings.Contains(contentType, "text/plain") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(body)
	} else if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") {
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	} else {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		w.Write([]byte("unsupported content type"))
		return
	}
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
	l = l.With("at-uri", resp.Uri)
	l.Info("wrote record to PDS")

	l.Info("adding to knot")
	secret, err := db.GetRegistrationKey(rp.db, f.Knot)
	if err != nil {
		fail("Failed to add to knot.", err)
		return
	}

	ksClient, err := knotclient.NewSignedClient(f.Knot, secret, rp.config.Core.Dev)
	if err != nil {
		fail("Failed to add to knot.", err)
		return
	}

	ksResp, err := ksClient.AddCollaborator(f.OwnerDid(), f.Name, collaboratorIdent.DID.String())
	if err != nil {
		fail("Knot was unreachable.", err)
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		fail(fmt.Sprintf("Knot returned unexpected status code: %d.", ksResp.StatusCode), nil)
		return
	}

	tx, err := rp.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail("Failed to add collaborator.", err)
		return
	}
	defer func() {
		tx.Rollback()
		err = rp.enforcer.E.LoadPolicy()
		if err != nil {
			fail("Failed to add collaborator.", err)
		}
	}()

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

	rp.pages.HxRefresh(w)
}

func (rp *Repo) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)

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
		rp.pages.Notice(w, "settings-delete", "Failed to delete repository from PDS.")
		return
	}
	log.Println("removed repo record ", f.RepoAt().String())

	secret, err := db.GetRegistrationKey(rp.db, f.Knot)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", f.Knot, err)
		return
	}

	ksClient, err := knotclient.NewSignedClient(f.Knot, secret, rp.config.Core.Dev)
	if err != nil {
		log.Println("failed to create client to ", f.Knot)
		return
	}

	ksResp, err := ksClient.RemoveRepo(f.OwnerDid(), f.Name)
	if err != nil {
		log.Printf("failed to make request to %s: %s", f.Knot, err)
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		log.Println("failed to remove repo from knot, continuing anyway ", f.Knot)
	} else {
		log.Println("removed repo from knot ", f.Knot)
	}

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
		rp.pages.Notice(w, "settings-delete", "Failed to remove collaborators")
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
		rp.pages.Notice(w, "settings-delete", "Failed to update RBAC rules")
		return
	}

	// remove repo from db
	err = db.RemoveRepo(tx, f.OwnerDid(), f.Name)
	if err != nil {
		rp.pages.Notice(w, "settings-delete", "Failed to update appview")
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

	branch := r.FormValue("branch")
	if branch == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	secret, err := db.GetRegistrationKey(rp.db, f.Knot)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", f.Knot, err)
		return
	}

	ksClient, err := knotclient.NewSignedClient(f.Knot, secret, rp.config.Core.Dev)
	if err != nil {
		log.Println("failed to create client to ", f.Knot)
		return
	}

	ksResp, err := ksClient.SetDefaultBranch(f.OwnerDid(), f.Name, branch)
	if err != nil {
		log.Printf("failed to make request to %s: %s", f.Knot, err)
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		rp.pages.Notice(w, "repo-settings", "Failed to set default branch. Try again later.")
		return
	}

	w.Write(fmt.Append(nil, "default branch set to: ", branch))
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

	// user := rp.oauth.GetUser(r)
	// repoCollaborators, err := f.Collaborators(r.Context())
	// if err != nil {
	// 	log.Println("failed to get collaborators", err)
	// }

	// isCollaboratorInviteAllowed := false
	// if user != nil {
	// 	ok, err := rp.enforcer.IsCollaboratorInviteAllowed(user.Did, f.Knot, f.DidSlashRepo())
	// 	if err == nil && ok {
	// 		isCollaboratorInviteAllowed = true
	// 	}
	// }

	// us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
	// if err != nil {
	// 	log.Println("failed to create unsigned client", err)
	// 	return
	// }

	// result, err := us.Branches(f.OwnerDid(), f.Name)
	// if err != nil {
	// 	log.Println("failed to reach knotserver", err)
	// 	return
	// }

	// // all spindles that this user is a member of
	// spindles, err := rp.enforcer.GetSpindlesForUser(user.Did)
	// if err != nil {
	// 	log.Println("failed to fetch spindles", err)
	// 	return
	// }

	// var secrets []*tangled.RepoListSecrets_Secret
	// if f.Spindle != "" {
	// 	if spindleClient, err := rp.oauth.ServiceClient(
	// 		r,
	// 		oauth.WithService(f.Spindle),
	// 		oauth.WithLxm(tangled.RepoListSecretsNSID),
	// 		oauth.WithDev(rp.config.Core.Dev),
	// 	); err != nil {
	// 		log.Println("failed to create spindle client", err)
	// 	} else if resp, err := tangled.RepoListSecrets(r.Context(), spindleClient, f.RepoAt().String()); err != nil {
	// 		log.Println("failed to fetch secrets", err)
	// 	} else {
	// 		secrets = resp.Secrets
	// 	}
	// }

	// rp.pages.RepoSettings(w, pages.RepoSettingsParams{
	// 	LoggedInUser:                user,
	// 	RepoInfo:                    f.RepoInfo(user),
	// 	Collaborators:               repoCollaborators,
	// 	IsCollaboratorInviteAllowed: isCollaboratorInviteAllowed,
	// 	Branches:                    result.Branches,
	// 	Spindles:                    spindles,
	// 	CurrentSpindle:              f.Spindle,
	// 	Secrets:                     secrets,
	// })
}

func (rp *Repo) generalSettings(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	user := rp.oauth.GetUser(r)

	us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
	if err != nil {
		log.Println("failed to create unsigned client", err)
		return
	}

	result, err := us.Branches(f.OwnerDid(), f.Name)
	if err != nil {
		log.Println("failed to reach knotserver", err)
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
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Printf("failed to resolve source repo: %v", err)
		return
	}

	switch r.Method {
	case http.MethodPost:
		secret, err := db.GetRegistrationKey(rp.db, f.Knot)
		if err != nil {
			rp.pages.Notice(w, "repo", fmt.Sprintf("No registration key found for knot %s.", f.Knot))
			return
		}

		client, err := knotclient.NewSignedClient(f.Knot, secret, rp.config.Core.Dev)
		if err != nil {
			rp.pages.Notice(w, "repo", "Failed to reach knot server.")
			return
		}

		var uri string
		if rp.config.Core.Dev {
			uri = "http"
		} else {
			uri = "https"
		}
		forkName := fmt.Sprintf("%s", f.Name)
		forkSourceUrl := fmt.Sprintf("%s://%s/%s/%s", uri, f.Knot, f.OwnerDid(), f.Repo.Name)

		_, err = client.SyncRepoFork(user.Did, forkSourceUrl, forkName, f.Ref)
		if err != nil {
			rp.pages.Notice(w, "repo", "Failed to sync repository fork.")
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

		knot := r.FormValue("knot")
		if knot == "" {
			rp.pages.Notice(w, "repo", "Invalid form submission&mdash;missing knot domain.")
			return
		}

		ok, err := rp.enforcer.E.Enforce(user.Did, knot, knot, "repo:create")
		if err != nil || !ok {
			rp.pages.Notice(w, "repo", "You do not have permission to create a repo in this knot.")
			return
		}

		forkName := fmt.Sprintf("%s", f.Name)

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
		secret, err := db.GetRegistrationKey(rp.db, knot)
		if err != nil {
			rp.pages.Notice(w, "repo", fmt.Sprintf("No registration key found for knot %s.", knot))
			return
		}

		client, err := knotclient.NewSignedClient(knot, secret, rp.config.Core.Dev)
		if err != nil {
			rp.pages.Notice(w, "repo", "Failed to reach knot server.")
			return
		}

		var uri string
		if rp.config.Core.Dev {
			uri = "http"
		} else {
			uri = "https"
		}
		forkSourceUrl := fmt.Sprintf("%s://%s/%s/%s", uri, f.Knot, f.OwnerDid(), f.Repo.Name)
		sourceAt := f.RepoAt().String()

		rkey := tid.TID()
		repo := &db.Repo{
			Did:    user.Did,
			Name:   forkName,
			Knot:   knot,
			Rkey:   rkey,
			Source: sourceAt,
		}

		tx, err := rp.db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Println(err)
			rp.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}
		defer func() {
			tx.Rollback()
			err = rp.enforcer.E.LoadPolicy()
			if err != nil {
				log.Println("failed to rollback policies")
			}
		}()

		resp, err := client.ForkRepo(user.Did, forkSourceUrl, forkName)
		if err != nil {
			rp.pages.Notice(w, "repo", "Failed to create repository on knot server.")
			return
		}

		switch resp.StatusCode {
		case http.StatusConflict:
			rp.pages.Notice(w, "repo", "A repository with that name already exists.")
			return
		case http.StatusInternalServerError:
			rp.pages.Notice(w, "repo", "Failed to create repository on knot. Try again later.")
		case http.StatusNoContent:
			// continue
		}

		xrpcClient, err := rp.oauth.AuthorizedClient(r)
		if err != nil {
			log.Println("failed to get authorized client", err)
			rp.pages.Notice(w, "repo", "Failed to create repository.")
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
			log.Printf("failed to create record: %s", err)
			rp.pages.Notice(w, "repo", "Failed to announce repository creation.")
			return
		}
		log.Println("created repo record: ", atresp.Uri)

		err = db.AddRepo(tx, repo)
		if err != nil {
			log.Println(err)
			rp.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// acls
		p, _ := securejoin.SecureJoin(user.Did, forkName)
		err = rp.enforcer.AddRepo(user.Did, knot, p)
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

		rp.pages.HxLocation(w, fmt.Sprintf("/@%s/%s", user.Handle, forkName))
		return
	}
}

func (rp *Repo) RepoCompareNew(w http.ResponseWriter, r *http.Request) {
	user := rp.oauth.GetUser(r)
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", f.Knot)
		rp.pages.Error503(w)
		return
	}

	result, err := us.Branches(f.OwnerDid(), f.Name)
	if err != nil {
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		log.Println("failed to reach knotserver", err)
		return
	}
	branches := result.Branches

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

	tags, err := us.Tags(f.OwnerDid(), f.Name)
	if err != nil {
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		log.Println("failed to reach knotserver", err)
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

	us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s", f.Knot)
		rp.pages.Error503(w)
		return
	}

	branches, err := us.Branches(f.OwnerDid(), f.Name)
	if err != nil {
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		log.Println("failed to reach knotserver", err)
		return
	}

	tags, err := us.Tags(f.OwnerDid(), f.Name)
	if err != nil {
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		log.Println("failed to reach knotserver", err)
		return
	}

	formatPatch, err := us.Compare(f.OwnerDid(), f.Name, base, head)
	if err != nil {
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		log.Println("failed to compare", err)
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
