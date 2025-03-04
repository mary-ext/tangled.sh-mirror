package state

import (
	"fmt"
	"io"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/go-chi/chi/v5"
)

func (s *State) InfoRefs(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("resolvedId").(identity.Identity)
	knot := r.Context().Value("knot").(string)
	repo := chi.URLParam(r, "repo")

	uri := "https"
	if s.config.Dev {
		uri = "http"
	}
	targetURL := fmt.Sprintf("%s://%s/%s/%s/info/refs?%s", uri, knot, user.DID, repo, r.URL.RawQuery)
	resp, err := http.Get(targetURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, v := range resp.Header {
		w.Header()[k] = v
	}

	// Set response status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	if _, err := io.Copy(w, resp.Body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

}

func (s *State) UploadPack(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		http.Error(w, "failed to resolve user", http.StatusInternalServerError)
		return
	}
	knot := r.Context().Value("knot").(string)
	repo := chi.URLParam(r, "repo")
	targetURL := fmt.Sprintf("https://%s/%s/%s/git-upload-pack?%s", knot, user.DID, repo, r.URL.RawQuery)
	client := &http.Client{}

	// Create new request
	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Copy original headers
	proxyReq.Header = r.Header

	// Execute request
	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, v := range resp.Header {
		w.Header()[k] = v
	}

	// Set response status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	if _, err := io.Copy(w, resp.Body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
