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

	"github.com/bluesky-social/indigo/atproto/data"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/auth"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/appview/pagination"
	"tangled.sh/tangled.sh/core/types"

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

	emails := uniqueEmails(result.Commits)

	user := s.auth.GetUser(r)
	s.pages.RepoIndexPage(w, pages.RepoIndexParams{
		LoggedInUser:       user,
		RepoInfo:           f.RepoInfo(s, user),
		TagMap:             tagMap,
		RepoIndexResponse:  result,
		EmailToDidOrHandle: EmailToDidOrHandle(s, emails),
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
		LoggedInUser:       user,
		RepoInfo:           f.RepoInfo(s, user),
		RepoLogResponse:    repolog,
		EmailToDidOrHandle: EmailToDidOrHandle(s, uniqueEmails(repolog.Commits)),
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
					CreatedAt:   f.CreatedAt,
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

	if !plumbing.IsHash(ref) {
		s.pages.Error404(w)
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

	user := s.auth.GetUser(r)
	s.pages.RepoCommit(w, pages.RepoCommitParams{
		LoggedInUser:       user,
		RepoInfo:           f.RepoInfo(s, user),
		RepoCommitResponse: result,
		EmailToDidOrHandle: EmailToDidOrHandle(s, []string{result.Diff.Commit.Author.Email}),
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

	user := s.auth.GetUser(r)
	s.pages.RepoBlob(w, pages.RepoBlobParams{
		LoggedInUser:     user,
		RepoInfo:         f.RepoInfo(s, user),
		RepoBlobResponse: result,
		BreadCrumbs:      breadcrumbs,
		ShowRendered:     showRendered,
		RenderToggle:     renderToggle,
	})
	return
}

func (s *State) RepoBlobRaw(w http.ResponseWriter, r *http.Request) {
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

	err = s.enforcer.AddCollaborator(collaboratorIdent.DID.String(), f.Knot, f.DidSlashRepo())
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

func (s *State) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)

	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	// remove record from pds
	xrpcClient, _ := s.auth.AuthorizedClient(r)
	repoRkey := f.RepoAt.RecordKey().String()
	_, err = comatproto.RepoDeleteRecord(r.Context(), xrpcClient, &comatproto.RepoDeleteRecord_Input{
		Collection: tangled.RepoNSID,
		Repo:       user.Did,
		Rkey:       repoRkey,
	})
	if err != nil {
		log.Printf("failed to delete record: %s", err)
		s.pages.Notice(w, "settings-delete", "Failed to delete repository from PDS.")
		return
	}
	log.Println("removed repo record ", f.RepoAt.String())

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

	// remove collaborator RBAC
	repoCollaborators, err := s.enforcer.E.GetImplicitUsersForResourceByDomain(f.DidSlashRepo(), f.Knot)
	if err != nil {
		s.pages.Notice(w, "settings-delete", "Failed to remove collaborators")
		return
	}
	for _, c := range repoCollaborators {
		did := c[0]
		s.enforcer.RemoveCollaborator(did, f.Knot, f.DidSlashRepo())
	}
	log.Println("removed collaborators")

	// remove repo RBAC
	err = s.enforcer.RemoveRepo(f.OwnerDid(), f.Knot, f.DidSlashRepo())
	if err != nil {
		s.pages.Notice(w, "settings-delete", "Failed to update RBAC rules")
		return
	}

	// remove repo from db
	err = db.RemoveRepo(tx, f.OwnerDid(), f.RepoName)
	if err != nil {
		s.pages.Notice(w, "settings-delete", "Failed to update appview")
		return
	}
	log.Println("removed repo from db")

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

	s.pages.HxRedirect(w, fmt.Sprintf("/%s", f.OwnerDid()))
}

func (s *State) SetDefaultBranch(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	branch := r.FormValue("branch")
	if branch == "" {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}

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

	ksResp, err := ksClient.SetDefaultBranch(f.OwnerDid(), f.RepoName, branch)
	if err != nil {
		log.Printf("failed to make request to %s: %s", f.Knot, err)
		return
	}

	if ksResp.StatusCode != http.StatusNoContent {
		s.pages.Notice(w, "repo-settings", "Failed to set default branch. Try again later.")
		return
	}

	w.Write([]byte(fmt.Sprint("default branch set to: ", branch)))
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
			ok, err := s.enforcer.IsCollaboratorInviteAllowed(user.Did, f.Knot, f.DidSlashRepo())
			if err == nil && ok {
				isCollaboratorInviteAllowed = true
			}
		}

		var branchNames []string
		var defaultBranch string
		us, err := NewUnsignedClient(f.Knot, s.config.Dev)
		if err != nil {
			log.Println("failed to create unsigned client", err)
		} else {
			resp, err := us.Branches(f.OwnerDid(), f.RepoName)
			if err != nil {
				log.Println("failed to reach knotserver", err)
			} else {
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Printf("Error reading response body: %v", err)
				} else {
					var result types.RepoBranchesResponse
					err = json.Unmarshal(body, &result)
					if err != nil {
						log.Println("failed to parse response:", err)
					} else {
						for _, branch := range result.Branches {
							branchNames = append(branchNames, branch.Name)
						}
					}
				}
			}

			resp, err = us.DefaultBranch(f.OwnerDid(), f.RepoName)
			if err != nil {
				log.Println("failed to reach knotserver", err)
			} else {
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Printf("Error reading response body: %v", err)
				} else {
					var result types.RepoDefaultBranchResponse
					err = json.Unmarshal(body, &result)
					if err != nil {
						log.Println("failed to parse response:", err)
					} else {
						defaultBranch = result.Branch
					}
				}
			}
		}

		s.pages.RepoSettings(w, pages.RepoSettingsParams{
			LoggedInUser:                user,
			RepoInfo:                    f.RepoInfo(s, user),
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
	source, err := db.GetRepoSource(s.db, f.RepoAt)
	if errors.Is(err, sql.ErrNoRows) {
		source = ""
	} else if err != nil {
		log.Println("failed to get repo source for ", f.RepoAt, err)
	}

	var sourceRepo *db.Repo
	if source != "" {
		sourceRepo, err = db.GetRepoByAtUri(s.db, source)
		if err != nil {
			log.Println("failed to get repo by at uri", err)
		}
	}

	var sourceHandle *identity.Identity
	if sourceRepo != nil {
		sourceHandle, err = s.resolver.ResolveIdent(context.Background(), sourceRepo.Did)
		if err != nil {
			log.Println("failed to resolve source repo", err)
		}
	}

	knot := f.Knot
	var disableFork bool
	us, err := NewUnsignedClient(knot, s.config.Dev)
	if err != nil {
		log.Printf("failed to create unsigned client for %s: %v", knot, err)
	} else {
		resp, err := us.Branches(f.OwnerDid(), f.RepoName)
		if err != nil {
			log.Printf("failed to get branches for %s/%s: %v", f.OwnerDid(), f.RepoName, err)
		} else {
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("error reading branch response body: %v", err)
			} else {
				var branchesResp types.RepoBranchesResponse
				if err := json.Unmarshal(body, &branchesResp); err != nil {
					log.Printf("error parsing branch response: %v", err)
				} else {
					disableFork = false
				}

				if len(branchesResp.Branches) == 0 {
					disableFork = true
				}
			}
		}
	}

	if knot == "knot1.tangled.sh" {
		knot = "tangled.sh"
	}

	repoInfo := pages.RepoInfo{
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
		DisableFork: disableFork,
	}

	if sourceRepo != nil {
		repoInfo.Source = sourceRepo
		repoInfo.SourceHandle = sourceHandle.Handle.String()
	}

	return repoInfo
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

	if isCollaborator || isIssueOwner {
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

func (s *State) NewIssueComment(w http.ResponseWriter, r *http.Request) {
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

		commentId := mathrand.IntN(1000000)
		rkey := appview.TID()

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
			s.pages.Notice(w, "issue-comment", "Failed to create comment.")
			return
		}

		s.pages.HxLocation(w, fmt.Sprintf("/%s/issues/%d#comment-%d", f.OwnerSlashRepo(), issueIdInt, commentId))
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

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, err := db.GetIssue(s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		s.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	comment, err := db.GetComment(s.db, f.RepoAt, issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		return
	}

	identity, err := s.resolver.ResolveIdent(r.Context(), comment.OwnerDid)
	if err != nil {
		log.Println("failed to resolve did")
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
		RepoInfo:     f.RepoInfo(s, user),
		DidHandleMap: didHandleMap,
		Issue:        issue,
		Comment:      comment,
	})
}

func (s *State) EditIssueComment(w http.ResponseWriter, r *http.Request) {
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

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	issue, err := db.GetIssue(s.db, f.RepoAt, issueIdInt)
	if err != nil {
		log.Println("failed to get issue", err)
		s.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	comment, err := db.GetComment(s.db, f.RepoAt, issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		return
	}

	if comment.OwnerDid != user.Did {
		http.Error(w, "you are not the author of this comment", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.pages.EditIssueCommentFragment(w, pages.EditIssueCommentParams{
			LoggedInUser: user,
			RepoInfo:     f.RepoInfo(s, user),
			Issue:        issue,
			Comment:      comment,
		})
	case http.MethodPost:
		// extract form value
		newBody := r.FormValue("body")
		client, _ := s.auth.AuthorizedClient(r)
		rkey := comment.Rkey

		// optimistic update
		edited := time.Now()
		err = db.EditComment(s.db, comment.RepoAt, comment.Issue, comment.CommentId, newBody)
		if err != nil {
			log.Println("failed to perferom update-description query", err)
			s.pages.Notice(w, "repo-notice", "Failed to update description, try again later.")
			return
		}

		// rkey is optional, it was introduced later
		if comment.Rkey != "" {
			// update the record on pds
			ex, err := comatproto.RepoGetRecord(r.Context(), client, "", tangled.RepoIssueCommentNSID, user.Did, rkey)
			if err != nil {
				// failed to get record
				log.Println(err, rkey)
				s.pages.Notice(w, fmt.Sprintf("comment-%s-status", commentId), "Failed to update description, no record found on PDS.")
				return
			}
			value, _ := ex.Value.MarshalJSON() // we just did get record; it is valid json
			record, _ := data.UnmarshalJSON(value)

			repoAt := record["repo"].(string)
			issueAt := record["issue"].(string)
			createdAt := record["createdAt"].(string)
			commentIdInt64 := int64(commentIdInt)

			_, err = comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
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
			RepoInfo:     f.RepoInfo(s, user),
			DidHandleMap: didHandleMap,
			Issue:        issue,
			Comment:      comment,
		})
		return

	}

}

func (s *State) DeleteIssueComment(w http.ResponseWriter, r *http.Request) {
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
		s.pages.Notice(w, "issues", "Failed to load issue. Try again later.")
		return
	}

	commentId := chi.URLParam(r, "comment_id")
	commentIdInt, err := strconv.Atoi(commentId)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		log.Println("failed to parse issue id", err)
		return
	}

	comment, err := db.GetComment(s.db, f.RepoAt, issueIdInt, commentIdInt)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		return
	}

	if comment.OwnerDid != user.Did {
		http.Error(w, "you are not the author of this comment", http.StatusUnauthorized)
		return
	}

	if comment.Deleted != nil {
		http.Error(w, "comment already deleted", http.StatusBadRequest)
		return
	}

	// optimistic deletion
	deleted := time.Now()
	err = db.DeleteComment(s.db, f.RepoAt, issueIdInt, commentIdInt)
	if err != nil {
		log.Println("failed to delete comment")
		s.pages.Notice(w, fmt.Sprintf("comment-%s-status", commentId), "failed to delete comment")
		return
	}

	// delete from pds
	if comment.Rkey != "" {
		client, _ := s.auth.AuthorizedClient(r)
		_, err = comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.GraphFollowNSID,
			Repo:       user.Did,
			Rkey:       comment.Rkey,
		})
		if err != nil {
			log.Println(err)
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
		RepoInfo:     f.RepoInfo(s, user),
		DidHandleMap: didHandleMap,
		Issue:        issue,
		Comment:      comment,
	})
	return
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

	page, ok := r.Context().Value("page").(pagination.Page)
	if !ok {
		log.Println("failed to get page")
		page = pagination.FirstPage()
	}

	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	issues, err := db.GetIssues(s.db, f.RepoAt, isOpen, page)
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
		Page:            page,
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
			Rkey:       appview.TID(),
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

func (s *State) ForkRepo(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Printf("failed to resolve source repo: %v", err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		user := s.auth.GetUser(r)
		knots, err := s.enforcer.GetDomainsForUser(user.Did)
		if err != nil {
			s.pages.Notice(w, "repo", "Invalid user account.")
			return
		}

		s.pages.ForkRepo(w, pages.ForkRepoParams{
			LoggedInUser: user,
			Knots:        knots,
			RepoInfo:     f.RepoInfo(s, user),
		})

	case http.MethodPost:

		knot := r.FormValue("knot")
		if knot == "" {
			s.pages.Notice(w, "repo", "Invalid form submission&mdash;missing knot domain.")
			return
		}

		ok, err := s.enforcer.E.Enforce(user.Did, knot, knot, "repo:create")
		if err != nil || !ok {
			s.pages.Notice(w, "repo", "You do not have permission to create a repo in this knot.")
			return
		}

		forkName := fmt.Sprintf("%s", f.RepoName)

		// this check is *only* to see if the forked repo name already exists
		// in the user's account.
		existingRepo, err := db.GetRepo(s.db, user.Did, f.RepoName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// no existing repo with this name found, we can use the name as is
			} else {
				log.Println("error fetching existing repo from db", err)
				s.pages.Notice(w, "repo", "Failed to fork this repository. Try again later.")
				return
			}
		} else if existingRepo != nil {
			// repo with this name already exists, append random string
			forkName = fmt.Sprintf("%s-%s", forkName, randomString(3))
		}
		secret, err := db.GetRegistrationKey(s.db, knot)
		if err != nil {
			s.pages.Notice(w, "repo", fmt.Sprintf("No registration key found for knot %s.", knot))
			return
		}

		client, err := NewSignedClient(knot, secret, s.config.Dev)
		if err != nil {
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

		rkey := appview.TID()
		repo := &db.Repo{
			Did:    user.Did,
			Name:   forkName,
			Knot:   knot,
			Rkey:   rkey,
			Source: sourceAt,
		}

		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Println(err)
			s.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}
		defer func() {
			tx.Rollback()
			err = s.enforcer.E.LoadPolicy()
			if err != nil {
				log.Println("failed to rollback policies")
			}
		}()

		resp, err := client.ForkRepo(user.Did, forkSourceUrl, forkName)
		if err != nil {
			s.pages.Notice(w, "repo", "Failed to create repository on knot server.")
			return
		}

		switch resp.StatusCode {
		case http.StatusConflict:
			s.pages.Notice(w, "repo", "A repository with that name already exists.")
			return
		case http.StatusInternalServerError:
			s.pages.Notice(w, "repo", "Failed to create repository on knot. Try again later.")
		case http.StatusNoContent:
			// continue
		}

		xrpcClient, _ := s.auth.AuthorizedClient(r)

		createdAt := time.Now().Format(time.RFC3339)
		atresp, err := comatproto.RepoPutRecord(r.Context(), xrpcClient, &comatproto.RepoPutRecord_Input{
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
			s.pages.Notice(w, "repo", "Failed to announce repository creation.")
			return
		}
		log.Println("created repo record: ", atresp.Uri)

		repo.AtUri = atresp.Uri
		err = db.AddRepo(tx, repo)
		if err != nil {
			log.Println(err)
			s.pages.Notice(w, "repo", "Failed to save repository information.")
			return
		}

		// acls
		p, _ := securejoin.SecureJoin(user.Did, forkName)
		err = s.enforcer.AddRepo(user.Did, knot, p)
		if err != nil {
			log.Println(err)
			s.pages.Notice(w, "repo", "Failed to set up repository permissions.")
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

		s.pages.HxLocation(w, fmt.Sprintf("/@%s/%s", user.Handle, forkName))
		return
	}
}
