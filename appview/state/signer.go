package state

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"tangled.sh/tangled.sh/core/types"
)

type SignerTransport struct {
	Secret string
}

func (s SignerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	timestamp := time.Now().Format(time.RFC3339)
	mac := hmac.New(sha256.New, []byte(s.Secret))
	message := req.Method + req.URL.Path + timestamp
	mac.Write([]byte(message))
	signature := hex.EncodeToString(mac.Sum(nil))
	req.Header.Set("X-Signature", signature)
	req.Header.Set("X-Timestamp", timestamp)
	return http.DefaultTransport.RoundTrip(req)
}

type SignedClient struct {
	Secret string
	Url    *url.URL
	client *http.Client
}

func NewSignedClient(domain, secret string, dev bool) (*SignedClient, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: SignerTransport{
			Secret: secret,
		},
	}

	scheme := "https"
	if dev {
		scheme = "http"
	}
	url, err := url.Parse(fmt.Sprintf("%s://%s", scheme, domain))
	if err != nil {
		return nil, err
	}

	signedClient := &SignedClient{
		Secret: secret,
		client: client,
		Url:    url,
	}

	return signedClient, nil
}

func (s *SignedClient) newRequest(method, endpoint string, body []byte) (*http.Request, error) {
	return http.NewRequest(method, s.Url.JoinPath(endpoint).String(), bytes.NewReader(body))
}

func (s *SignedClient) Init(did string) (*http.Response, error) {
	const (
		Method   = "POST"
		Endpoint = "/init"
	)

	body, _ := json.Marshal(map[string]any{
		"did": did,
	})

	req, err := s.newRequest(Method, Endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) NewRepo(did, repoName, defaultBranch string) (*http.Response, error) {
	const (
		Method   = "PUT"
		Endpoint = "/repo/new"
	)

	body, _ := json.Marshal(map[string]any{
		"did":            did,
		"name":           repoName,
		"default_branch": defaultBranch,
	})

	req, err := s.newRequest(Method, Endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) ForkRepo(ownerDid, source, name string) (*http.Response, error) {
	const (
		Method   = "POST"
		Endpoint = "/repo/fork"
	)

	body, _ := json.Marshal(map[string]any{
		"did":    ownerDid,
		"source": source,
		"name":   name,
	})

	req, err := s.newRequest(Method, Endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) RemoveRepo(did, repoName string) (*http.Response, error) {
	const (
		Method   = "DELETE"
		Endpoint = "/repo"
	)

	body, _ := json.Marshal(map[string]any{
		"did":  did,
		"name": repoName,
	})

	req, err := s.newRequest(Method, Endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) AddMember(did string) (*http.Response, error) {
	const (
		Method   = "PUT"
		Endpoint = "/member/add"
	)

	body, _ := json.Marshal(map[string]any{
		"did": did,
	})

	req, err := s.newRequest(Method, Endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) SetDefaultBranch(ownerDid, repoName, branch string) (*http.Response, error) {
	const (
		Method = "PUT"
	)
	endpoint := fmt.Sprintf("/%s/%s/branches/default", ownerDid, repoName)

	body, _ := json.Marshal(map[string]any{
		"branch": branch,
	})

	req, err := s.newRequest(Method, endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) AddCollaborator(ownerDid, repoName, memberDid string) (*http.Response, error) {
	const (
		Method = "POST"
	)
	endpoint := fmt.Sprintf("/%s/%s/collaborator/add", ownerDid, repoName)

	body, _ := json.Marshal(map[string]any{
		"did": memberDid,
	})

	req, err := s.newRequest(Method, endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) Merge(
	patch []byte,
	ownerDid, targetRepo, branch, commitMessage, commitBody, authorName, authorEmail string,
) (*http.Response, error) {
	const (
		Method = "POST"
	)
	endpoint := fmt.Sprintf("/%s/%s/merge", ownerDid, targetRepo)

	mr := types.MergeRequest{
		Branch:        branch,
		CommitMessage: commitMessage,
		CommitBody:    commitBody,
		AuthorName:    authorName,
		AuthorEmail:   authorEmail,
		Patch:         string(patch),
	}

	body, _ := json.Marshal(mr)

	req, err := s.newRequest(Method, endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) MergeCheck(patch []byte, ownerDid, targetRepo, branch string) (*http.Response, error) {
	const (
		Method = "POST"
	)
	endpoint := fmt.Sprintf("/%s/%s/merge/check", ownerDid, targetRepo)

	body, _ := json.Marshal(map[string]any{
		"patch":  string(patch),
		"branch": branch,
	})

	req, err := s.newRequest(Method, endpoint, body)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

func (s *SignedClient) NewHiddenRef(ownerDid, targetRepo, forkBranch, remoteBranch string) (*http.Response, error) {
	const (
		Method = "POST"
	)
	endpoint := fmt.Sprintf("/%s/%s/hidden-ref/%s/%s", ownerDid, targetRepo, url.PathEscape(forkBranch), url.PathEscape(remoteBranch))

	req, err := s.newRequest(Method, endpoint, nil)
	if err != nil {
		return nil, err
	}

	return s.client.Do(req)
}

type UnsignedClient struct {
	Url    *url.URL
	client *http.Client
}

func NewUnsignedClient(domain string, dev bool) (*UnsignedClient, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	scheme := "https"
	if dev {
		scheme = "http"
	}
	url, err := url.Parse(fmt.Sprintf("%s://%s", scheme, domain))
	if err != nil {
		return nil, err
	}

	unsignedClient := &UnsignedClient{
		client: client,
		Url:    url,
	}

	return unsignedClient, nil
}

func (us *UnsignedClient) newRequest(method, endpoint string, body []byte) (*http.Request, error) {
	return http.NewRequest(method, us.Url.JoinPath(endpoint).String(), bytes.NewReader(body))
}

func (us *UnsignedClient) Index(ownerDid, repoName, ref string) (*http.Response, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/tree/%s", ownerDid, repoName, ref)
	if ref == "" {
		endpoint = fmt.Sprintf("/%s/%s", ownerDid, repoName)
	}

	req, err := us.newRequest(Method, endpoint, nil)
	if err != nil {
		return nil, err
	}

	return us.client.Do(req)
}

func (us *UnsignedClient) Branches(ownerDid, repoName string) (*http.Response, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/branches", ownerDid, repoName)

	req, err := us.newRequest(Method, endpoint, nil)
	if err != nil {
		return nil, err
	}

	return us.client.Do(req)
}

func (us *UnsignedClient) Branch(ownerDid, repoName, branch string) (*http.Response, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/branches/%s", ownerDid, repoName, branch)

	req, err := us.newRequest(Method, endpoint, nil)
	if err != nil {
		return nil, err
	}

	return us.client.Do(req)
}

func (us *UnsignedClient) DefaultBranch(ownerDid, repoName string) (*http.Response, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/branches/default", ownerDid, repoName)

	req, err := us.newRequest(Method, endpoint, nil)
	if err != nil {
		return nil, err
	}

	return us.client.Do(req)
}

func (us *UnsignedClient) Capabilities() (*types.Capabilities, error) {
	const (
		Method   = "GET"
		Endpoint = "/capabilities"
	)

	req, err := us.newRequest(Method, Endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := us.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var capabilities types.Capabilities
	if err := json.NewDecoder(resp.Body).Decode(&capabilities); err != nil {
		return nil, err
	}

	return &capabilities, nil
}

func (us *UnsignedClient) Compare(ownerDid, repoName, rev1, rev2 string) (*types.RepoFormatPatchResponse, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/compare/%s/%s", ownerDid, repoName, url.PathEscape(rev1), url.PathEscape(rev2))

	req, err := us.newRequest(Method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to create request.")
	}

	compareResp, err := us.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Failed to create request.")
	}
	defer compareResp.Body.Close()

	switch compareResp.StatusCode {
	case 404:
	case 400:
		return nil, fmt.Errorf("Branch comparisons not supported on this knot.")
	}

	respBody, err := io.ReadAll(compareResp.Body)
	if err != nil {
		log.Println("failed to compare across branches")
		return nil, fmt.Errorf("Failed to compare branches.")
	}
	defer compareResp.Body.Close()

	var formatPatchResponse types.RepoFormatPatchResponse
	err = json.Unmarshal(respBody, &formatPatchResponse)
	if err != nil {
		log.Println("failed to unmarshal format-patch response", err)
		return nil, fmt.Errorf("failed to compare branches.")
	}

	return &formatPatchResponse, nil
}
