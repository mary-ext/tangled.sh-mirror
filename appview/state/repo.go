package state

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/bluesky-social/indigo/atproto/identity"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-chi/chi/v5"
	"github.com/sotangled/tangled/appview/auth"
	"github.com/sotangled/tangled/appview/pages"
	"github.com/sotangled/tangled/types"
)

func (s *State) RepoIndex(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		return
	}
	var reqUrl string
	if ref != "" {
		reqUrl = fmt.Sprintf("http://%s/%s/%s/tree/%s", f.Knot, f.OwnerDid(), f.RepoName, ref)
	} else {
		reqUrl = fmt.Sprintf("http://%s/%s/%s", f.Knot, f.OwnerDid(), f.RepoName)
	}

	resp, err := http.Get(reqUrl)
	if err != nil {
		s.pages.Error503(w)
		log.Println("failed to reach knotserver", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
		return
	}

	var result types.RepoIndexResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Fatalf("Error unmarshalling response body: %v", err)
		return
	}

	log.Println(resp.Status, result)

	user := s.auth.GetUser(r)
	s.pages.RepoIndexPage(w, pages.RepoIndexParams{
		LoggedInUser: user,
		RepoInfo: pages.RepoInfo{
			OwnerDid:        f.OwnerDid(),
			OwnerHandle:     f.OwnerHandle(),
			Name:            f.RepoName,
			SettingsAllowed: settingsAllowed(s, user, f),
		},
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

	ref := chi.URLParam(r, "ref")
	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/log/%s", f.Knot, f.OwnerDid(), f.RepoName, ref))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
		return
	}

	var result types.RepoLogResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse json response", err)
		return
	}

	user := s.auth.GetUser(r)
	s.pages.RepoLog(w, pages.RepoLogParams{
		LoggedInUser: user,
		RepoInfo: pages.RepoInfo{
			OwnerDid:        f.OwnerDid(),
			OwnerHandle:     f.OwnerHandle(),
			Name:            f.RepoName,
			SettingsAllowed: settingsAllowed(s, user, f),
		},
		RepoLogResponse: result,
	})
	return
}

func (s *State) RepoCommit(w http.ResponseWriter, r *http.Request) {
	f, err := fullyResolvedRepo(r)
	if err != nil {
		log.Println("failed to fully resolve repo", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/commit/%s", f.Knot, f.OwnerDid(), f.RepoName, ref))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
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
		LoggedInUser: user,
		RepoInfo: pages.RepoInfo{
			OwnerDid:        f.OwnerDid(),
			OwnerHandle:     f.OwnerHandle(),
			Name:            f.RepoName,
			SettingsAllowed: settingsAllowed(s, user, f),
		},
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
	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/tree/%s/%s", f.Knot, f.OwnerDid(), f.RepoName, ref, treePath))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
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
		LoggedInUser: user,
		BreadCrumbs:  breadcrumbs,
		BaseTreeLink: baseTreeLink,
		BaseBlobLink: baseBlobLink,
		RepoInfo: pages.RepoInfo{
			OwnerDid:        f.OwnerDid(),
			OwnerHandle:     f.OwnerHandle(),
			Name:            f.RepoName,
			SettingsAllowed: settingsAllowed(s, user, f),
		},
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

	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/tags", f.Knot, f.OwnerDid(), f.RepoName))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
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
		LoggedInUser: user,
		RepoInfo: pages.RepoInfo{
			OwnerDid:        f.OwnerDid(),
			OwnerHandle:     f.OwnerHandle(),
			Name:            f.RepoName,
			SettingsAllowed: settingsAllowed(s, user, f),
		},
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

	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/branches", f.Knot, f.OwnerDid(), f.RepoName))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
		return
	}

	var result types.RepoBranchesResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Println("failed to parse response:", err)
		return
	}

	log.Println(result)

	user := s.auth.GetUser(r)
	s.pages.RepoBranches(w, pages.RepoBranchesParams{
		LoggedInUser: user,
		RepoInfo: pages.RepoInfo{
			OwnerDid:        f.OwnerDid(),
			OwnerHandle:     f.OwnerHandle(),
			Name:            f.RepoName,
			SettingsAllowed: settingsAllowed(s, user, f),
		},
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
	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/blob/%s/%s", f.Knot, f.OwnerDid(), f.RepoName, ref, filePath))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
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
		LoggedInUser: user,
		RepoInfo: pages.RepoInfo{
			OwnerDid:        f.OwnerDid(),
			OwnerHandle:     f.OwnerHandle(),
			Name:            f.RepoName,
			SettingsAllowed: settingsAllowed(s, user, f),
		},
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

	secret, err := s.db.GetRegistrationKey(f.Knot)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", f.Knot, err)
		return
	}

	ksClient, err := NewSignedClient(f.Knot, secret)
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

	err = s.enforcer.AddCollaborator(collaboratorIdent.DID.String(), f.Knot, f.OwnerSlashRepo())
	if err != nil {
		w.Write([]byte(fmt.Sprint("failed to add collaborator: ", err)))
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
		repoCollaborators, err := s.enforcer.E.GetImplicitUsersForResourceByDomain(f.OwnerSlashRepo(), f.Knot)
		if err != nil {
			log.Println("failed to get collaborators", err)
		}
		log.Println(repoCollaborators)

		isCollaboratorInviteAllowed := false
		if user != nil {
			ok, err := s.enforcer.IsCollaboratorInviteAllowed(user.Did, f.Knot, f.OwnerSlashRepo())
			if err == nil && ok {
				isCollaboratorInviteAllowed = true
			}
		}

		s.pages.RepoSettings(w, pages.RepoSettingsParams{
			LoggedInUser: user,
			RepoInfo: pages.RepoInfo{
				OwnerDid:        f.OwnerDid(),
				OwnerHandle:     f.OwnerHandle(),
				Name:            f.RepoName,
				SettingsAllowed: settingsAllowed(s, user, f),
			},
			Collaborators:               repoCollaborators,
			IsCollaboratorInviteAllowed: isCollaboratorInviteAllowed,
		})
	}
}

type FullyResolvedRepo struct {
	Knot     string
	OwnerId  identity.Identity
	RepoName string
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

	return &FullyResolvedRepo{
		Knot:     knot,
		OwnerId:  id,
		RepoName: repoName,
	}, nil
}

func settingsAllowed(s *State, u *auth.User, f *FullyResolvedRepo) bool {
	settingsAllowed := false
	if u != nil {
		ok, err := s.enforcer.IsSettingsAllowed(u.Did, f.Knot, f.OwnerSlashRepo())
		if err == nil && ok {
			settingsAllowed = true
		} else {
			log.Println(err, ok)
		}
	}

	return settingsAllowed
}
