package knotclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"tangled.sh/tangled.sh/core/types"
)

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

func (us *UnsignedClient) newRequest(method, endpoint string, query url.Values, body []byte) (*http.Request, error) {
	reqUrl := us.Url.JoinPath(endpoint)

	// add query parameters
	if query != nil {
		reqUrl.RawQuery = query.Encode()
	}

	return http.NewRequest(method, reqUrl.String(), bytes.NewReader(body))
}

func do[T any](us *UnsignedClient, req *http.Request) (*T, error) {
	resp, err := us.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return nil, err
	}

	var result T
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Printf("Error unmarshalling response body: %v", err)
		return nil, err
	}

	return &result, nil
}

func (us *UnsignedClient) Index(ownerDid, repoName, ref string) (*types.RepoIndexResponse, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/tree/%s", ownerDid, repoName, ref)
	if ref == "" {
		endpoint = fmt.Sprintf("/%s/%s", ownerDid, repoName)
	}

	req, err := us.newRequest(Method, endpoint, nil, nil)
	if err != nil {
		return nil, err
	}

	return do[types.RepoIndexResponse](us, req)
}

func (us *UnsignedClient) Log(ownerDid, repoName, ref string, page int) (*types.RepoLogResponse, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/log/%s", ownerDid, repoName, url.PathEscape(ref))

	query := url.Values{}
	query.Add("page", strconv.Itoa(page))
	query.Add("per_page", strconv.Itoa(60))

	req, err := us.newRequest(Method, endpoint, query, nil)
	if err != nil {
		return nil, err
	}

	return do[types.RepoLogResponse](us, req)
}

func (us *UnsignedClient) Branches(ownerDid, repoName string) (*types.RepoBranchesResponse, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/branches", ownerDid, repoName)

	req, err := us.newRequest(Method, endpoint, nil, nil)
	if err != nil {
		return nil, err
	}

	return do[types.RepoBranchesResponse](us, req)
}

func (us *UnsignedClient) Tags(ownerDid, repoName string) (*types.RepoTagsResponse, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/tags", ownerDid, repoName)

	req, err := us.newRequest(Method, endpoint, nil, nil)
	if err != nil {
		return nil, err
	}

	return do[types.RepoTagsResponse](us, req)
}

func (us *UnsignedClient) Branch(ownerDid, repoName, branch string) (*types.RepoBranchResponse, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/branches/%s", ownerDid, repoName, url.PathEscape(branch))

	req, err := us.newRequest(Method, endpoint, nil, nil)
	if err != nil {
		return nil, err
	}

	return do[types.RepoBranchResponse](us, req)
}

func (us *UnsignedClient) DefaultBranch(ownerDid, repoName string) (*types.RepoDefaultBranchResponse, error) {
	const (
		Method = "GET"
	)

	endpoint := fmt.Sprintf("/%s/%s/branches/default", ownerDid, repoName)

	req, err := us.newRequest(Method, endpoint, nil, nil)
	if err != nil {
		return nil, err
	}

	resp, err := us.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var defaultBranch types.RepoDefaultBranchResponse
	if err := json.NewDecoder(resp.Body).Decode(&defaultBranch); err != nil {
		return nil, err
	}

	return &defaultBranch, nil
}

func (us *UnsignedClient) Capabilities() (*types.Capabilities, error) {
	const (
		Method   = "GET"
		Endpoint = "/capabilities"
	)

	req, err := us.newRequest(Method, Endpoint, nil, nil)
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

	req, err := us.newRequest(Method, endpoint, nil, nil)
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

func (s *UnsignedClient) RepoLanguages(ownerDid, repoName, ref string) (*types.RepoLanguageResponse, error) {
	const (
		Method = "GET"
	)
	endpoint := fmt.Sprintf("/%s/%s/languages/%s", ownerDid, repoName, url.PathEscape(ref))

	req, err := s.newRequest(Method, endpoint, nil, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}

	var result types.RepoLanguageResponse
	if resp.StatusCode != http.StatusOK {
		log.Println("failed to calculate languages", resp.Status)
		return &types.RepoLanguageResponse{}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}
