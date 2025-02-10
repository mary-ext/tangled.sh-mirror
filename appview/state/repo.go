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
	ctx := r.Context()
	repoName := chi.URLParam(r, "repo")

	knot, ok := ctx.Value("knot").(string)
	if !ok {
		log.Println("malformed middleware")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	id, ok := ctx.Value("resolvedId").(identity.Identity)
	if !ok {
		log.Println("malformed middleware")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s", knot, id.DID.String(), repoName))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}
	defer resp.Body.Close()

	// Read the response body
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
		LoggedInUser:      user,
		UserDid:           id.DID.String(),
		UserHandle:        id.Handle.String(),
		Name:              repoName,
		RepoIndexResponse: result,
	})

	return
}
