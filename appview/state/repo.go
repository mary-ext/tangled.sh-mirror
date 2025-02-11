package state

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/go-chi/chi/v5"
	"github.com/sotangled/tangled/appview/pages"
	"github.com/sotangled/tangled/types"
)

func (s *State) RepoIndex(w http.ResponseWriter, r *http.Request) {
	repoName, knot, id, err := repoKnotAndId(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s", knot, id.DID.String(), repoName))
	if err != nil {
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

	s.pages.RepoIndexPage(w, pages.RepoIndexParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo: pages.RepoInfo{
			OwnerDid:    id.DID.String(),
			OwnerHandle: id.Handle.String(),
			Name:        repoName,
		},
		RepoIndexResponse: result,
	})

	return
}

func (s *State) RepoLog(w http.ResponseWriter, r *http.Request) {
	repoName, knot, id, err := repoKnotAndId(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/log/%s", knot, id.DID.String(), repoName, ref))
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

	s.pages.RepoLog(w, pages.RepoLogParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo: pages.RepoInfo{
			OwnerDid:    id.DID.String(),
			OwnerHandle: id.Handle.String(),
			Name:        repoName,
		},
		RepoLogResponse: result,
	})
	return
}

func (s *State) RepoCommit(w http.ResponseWriter, r *http.Request) {
	repoName, knot, id, err := repoKnotAndId(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/commit/%s", knot, id.DID.String(), repoName, ref))
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

	s.pages.RepoCommit(w, pages.RepoCommitParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo: pages.RepoInfo{
			OwnerDid:    id.DID.String(),
			OwnerHandle: id.Handle.String(),
			Name:        repoName,
		},
		RepoCommitResponse: result,
	})
	return
}

func (s *State) RepoTree(w http.ResponseWriter, r *http.Request) {
	repoName, knot, id, err := repoKnotAndId(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	treePath := chi.URLParam(r, "*")
	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/tree/%s/%s", knot, id.DID.String(), repoName, ref, treePath))
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

	log.Println(result)

	s.pages.RepoTree(w, pages.RepoTreeParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo: pages.RepoInfo{
			OwnerDid:    id.DID.String(),
			OwnerHandle: id.Handle.String(),
			Name:        repoName,
		},
		RepoTreeResponse: result,
	})
	return
}

func (s *State) RepoTags(w http.ResponseWriter, r *http.Request) {
	repoName, knot, id, err := repoKnotAndId(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/tags", knot, id.DID.String(), repoName))
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

	s.pages.RepoTags(w, pages.RepoTagsParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo: pages.RepoInfo{
			OwnerDid:    id.DID.String(),
			OwnerHandle: id.Handle.String(),
			Name:        repoName,
		},
		RepoTagsResponse: result,
	})
	return
}

func (s *State) RepoBranches(w http.ResponseWriter, r *http.Request) {
	repoName, knot, id, err := repoKnotAndId(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/branches", knot, id.DID.String(), repoName))
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

	s.pages.RepoBranches(w, pages.RepoBranchesParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo: pages.RepoInfo{
			OwnerDid:    id.DID.String(),
			OwnerHandle: id.Handle.String(),
			Name:        repoName,
		},
		RepoBranchesResponse: result,
	})
	return
}

func (s *State) RepoBlob(w http.ResponseWriter, r *http.Request) {
	repoName, knot, id, err := repoKnotAndId(r)
	if err != nil {
		log.Println("failed to get repo and knot", err)
		return
	}

	ref := chi.URLParam(r, "ref")
	filePath := chi.URLParam(r, "*")
	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s/blob/%s/%s", knot, id.DID.String(), repoName, ref, filePath))
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

	s.pages.RepoBlob(w, pages.RepoBlobParams{
		LoggedInUser: s.auth.GetUser(r),
		RepoInfo: pages.RepoInfo{
			OwnerDid:    id.DID.String(),
			OwnerHandle: id.Handle.String(),
			Name:        repoName,
		},
		RepoBlobResponse: result,
	})
	return
}

func repoKnotAndId(r *http.Request) (string, string, identity.Identity, error) {
	repoName := chi.URLParam(r, "repo")
	knot, ok := r.Context().Value("knot").(string)
	if !ok {
		log.Println("malformed middleware")
		return "", "", identity.Identity{}, fmt.Errorf("malformed middleware")
	}
	id, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		log.Println("malformed middleware")
		return "", "", identity.Identity{}, fmt.Errorf("malformed middleware")
	}

	return repoName, knot, id, nil
}
