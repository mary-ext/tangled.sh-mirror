package state

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/go-chi/chi/v5"
)

func (s *State) RepoIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	repoName := chi.URLParam(r, "repo")

	domain, ok := ctx.Value("domain").(string)
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

	resp, err := http.Get(fmt.Sprintf("http://%s/%s/%s", domain, id.DID.String(), repoName))
	if err != nil {
		log.Println("failed to reach knotserver", err)
		return
	}

	txt, err := io.ReadAll(resp.Body)
	log.Println(resp.Status, string(txt))

	return
}
