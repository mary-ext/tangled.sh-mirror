package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/commitverify"
	"tangled.sh/tangled.sh/core/appview/config"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/idresolver"
	"tangled.sh/tangled.sh/core/appview/notify"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/eventconsumer"
	"tangled.sh/tangled.sh/core/knotclient"
	"tangled.sh/tangled.sh/core/patchutil"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/types"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
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
	}
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

	repolog, err := us.Log(f.OwnerDid(), f.RepoName, ref, page)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	tagResult, err := us.Tags(f.OwnerDid(), f.RepoName)
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

	branchResult, err := us.Branches(f.OwnerDid(), f.RepoName)
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
	return
}

func (rp *Repo) RepoDescription(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
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
					Name:        f.RepoName,
					Owner:       user.Did,
					CreatedAt:   f.CreatedAt,
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

	// redirects tree paths trying to access a blob; in this case the result.Files is unpopulated,
	// so we can safely redirect to the "parent" (which is the same file).
	unescapedTreePath, _ := url.PathUnescape(treePath)
	if len(result.Files) == 0 && result.Parent == unescapedTreePath {
		http.Redirect(w, r, fmt.Sprintf("/%s/blob/%s/%s", f.OwnerSlashRepo(), ref, result.Parent), http.StatusFound)
		return
	}

	user := rp.oauth.GetUser(r)

	var breadcrumbs [][]string
	breadcrumbs = append(breadcrumbs, []string{f.RepoName, fmt.Sprintf("/%s/tree/%s", f.OwnerSlashRepo(), ref)})
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

	result, err := us.Tags(f.OwnerDid(), f.RepoName)
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	artifacts, err := db.GetArtifact(rp.db, db.FilterEq("repo_at", f.RepoAt))
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
	return
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

	result, err := us.Branches(f.OwnerDid(), f.RepoName)
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

	user := rp.oauth.GetUser(r)
	rp.pages.RepoBlob(w, pages.RepoBlobParams{
		LoggedInUser:     user,
		RepoInfo:         f.RepoInfo(user),
		RepoBlobResponse: result,
		BreadCrumbs:      breadcrumbs,
		ShowRendered:     showRendered,
		RenderToggle:     renderToggle,
	})
	return
}

func (rp *Repo) RepoBlobRaw(w http.ResponseWriter, r *http.Request) {
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

	if result.IsBinary {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(result.Contents))
	return
}

// modify the spindle configured for this repo
func (rp *Repo) EditSpindle(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
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

	user := rp.oauth.GetUser(r)

	newSpindle := r.FormValue("spindle")
	client, err := rp.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to get client")
		rp.pages.Notice(w, "repo-notice", "Failed to configure spindle, try again later.")
		return
	}

	// ensure that this is a valid spindle for this user
	validSpindles, err := rp.enforcer.GetSpindlesForUser(user.Did)
	if err != nil {
		log.Println("failed to get valid spindles")
		rp.pages.Notice(w, "repo-notice", "Failed to configure spindle, try again later.")
		return
	}

	if !slices.Contains(validSpindles, newSpindle) {
		log.Println("newSpindle not present in validSpindles", "newSpindle", newSpindle, "validSpindles", validSpindles)
		rp.pages.Notice(w, "repo-notice", "Failed to configure spindle, try again later.")
		return
	}

	// optimistic update
	err = db.UpdateSpindle(rp.db, string(repoAt), newSpindle)
	if err != nil {
		log.Println("failed to perform update-spindle query", err)
		rp.pages.Notice(w, "repo-notice", "Failed to configure spindle, try again later.")
		return
	}

	ex, err := client.RepoGetRecord(r.Context(), "", tangled.RepoNSID, user.Did, rkey)
	if err != nil {
		// failed to get record
		rp.pages.Notice(w, "repo-notice", "Failed to configure spindle, no record found on PDS.")
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
				Name:        f.RepoName,
				Owner:       user.Did,
				CreatedAt:   f.CreatedAt,
				Description: &f.Description,
				Spindle:     &newSpindle,
			},
		},
	})

	if err != nil {
		log.Println("failed to perform update-spindle query", err)
		// failed to get record
		rp.pages.Notice(w, "repo-notice", "Failed to configure spindle, unable to save to PDS.")
		return
	}

	// add this spindle to spindle stream
	rp.spindlestream.AddSource(
		context.Background(),
		eventconsumer.NewSpindleSource(newSpindle),
	)

	w.Write(fmt.Append(nil, "spindle set to: ", newSpindle))
}

func (rp *Repo) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	collaborator := r.FormValue("collaborator")
	if collaborator == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

	collaboratorIdent, err := rp.idResolver.ResolveIdent(r.Context(), collaborator)
	if err != nil {
		w.Write([]byte("failed to resolve collaborator did to a handle"))
		return
	}
	log.Printf("adding %s to %s\n", collaboratorIdent.Handle.String(), f.Knot)

	// TODO: create an atproto record for this

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

	ksResp, err := ksClient.AddCollaborator(f.OwnerDid(), f.RepoName, collaboratorIdent.DID.String())
	if err != nil {
		log.Printf("failed to make request to %s: %s", f.Knot, err)
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		w.Write(fmt.Append(nil, "knotserver failed to add collaborator: ", err))
		return
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

	err = rp.enforcer.AddCollaborator(collaboratorIdent.DID.String(), f.Knot, f.DidSlashRepo())
	if err != nil {
		w.Write(fmt.Append(nil, "failed to add collaborator: ", err))
		return
	}

	err = db.AddCollaborator(rp.db, collaboratorIdent.DID.String(), f.OwnerDid(), f.RepoName, f.Knot)
	if err != nil {
		w.Write(fmt.Append(nil, "failed to add collaborator: ", err))
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

	w.Write(fmt.Append(nil, "added collaborator: ", collaboratorIdent.Handle.String()))

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
	repoRkey := f.RepoAt.RecordKey().String()
	_, err = xrpcClient.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       user.Did,
		Rkey:       repoRkey,
	})
	if err != nil {
		log.Printf("failed to delete record: %s", err)
		rp.pages.Notice(w, "settings-delete", "Failed to delete repository from PDS.")
		return
	}
	log.Println("removed repo record ", f.RepoAt.String())

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

	ksResp, err := ksClient.RemoveRepo(f.OwnerDid(), f.RepoName)
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
	err = db.RemoveRepo(tx, f.OwnerDid(), f.RepoName)
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

	ksResp, err := ksClient.SetDefaultBranch(f.OwnerDid(), f.RepoName, branch)
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

func (rp *Repo) RepoSettings(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// for now, this is just pubkeys
		user := rp.oauth.GetUser(r)
		repoCollaborators, err := f.Collaborators(r.Context())
		if err != nil {
			log.Println("failed to get collaborators", err)
		}

		isCollaboratorInviteAllowed := false
		if user != nil {
			ok, err := rp.enforcer.IsCollaboratorInviteAllowed(user.Did, f.Knot, f.DidSlashRepo())
			if err == nil && ok {
				isCollaboratorInviteAllowed = true
			}
		}

		us, err := knotclient.NewUnsignedClient(f.Knot, rp.config.Core.Dev)
		if err != nil {
			log.Println("failed to create unsigned client", err)
			return
		}

		result, err := us.Branches(f.OwnerDid(), f.RepoName)
		if err != nil {
			log.Println("failed to reach knotserver", err)
			return
		}

		// all spindles that this user is a member of
		spindles, err := rp.enforcer.GetSpindlesForUser(user.Did)
		if err != nil {
			log.Println("failed to fetch spindles", err)
			return
		}

		rp.pages.RepoSettings(w, pages.RepoSettingsParams{
			LoggedInUser:                user,
			RepoInfo:                    f.RepoInfo(user),
			Collaborators:               repoCollaborators,
			IsCollaboratorInviteAllowed: isCollaboratorInviteAllowed,
			Branches:                    result.Branches,
			Spindles:                    spindles,
			CurrentSpindle:              f.Spindle,
		})
	}
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
		forkName := fmt.Sprintf("%s", f.RepoName)
		forkSourceUrl := fmt.Sprintf("%s://%s/%s/%s", uri, f.Knot, f.OwnerDid(), f.RepoName)

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

		forkName := fmt.Sprintf("%s", f.RepoName)

		// this check is *only* to see if the forked repo name already exists
		// in the user's account.
		existingRepo, err := db.GetRepo(rp.db, user.Did, f.RepoName)
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
		forkSourceUrl := fmt.Sprintf("%s://%s/%s/%s", uri, f.Knot, f.OwnerDid(), f.RepoName)
		sourceAt := f.RepoAt.String()

		rkey := appview.TID()
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

		repo.AtUri = atresp.Uri
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

	result, err := us.Branches(f.OwnerDid(), f.RepoName)
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

	tags, err := us.Tags(f.OwnerDid(), f.RepoName)
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

	branches, err := us.Branches(f.OwnerDid(), f.RepoName)
	if err != nil {
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		log.Println("failed to reach knotserver", err)
		return
	}

	tags, err := us.Tags(f.OwnerDid(), f.RepoName)
	if err != nil {
		rp.pages.Notice(w, "compare-error", "Failed to produce comparison. Try again later.")
		log.Println("failed to reach knotserver", err)
		return
	}

	formatPatch, err := us.Compare(f.OwnerDid(), f.RepoName, base, head)
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
