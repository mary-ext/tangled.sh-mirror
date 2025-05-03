package knotclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"

	"tangled.sh/tangled.sh/core/types"
)

type UnsignedClient struct {
	Domain string
	Dev    bool
	client *http.Client
}

func (u *UnsignedClient) scheme() string {
	if u.Dev {
		return "http"
	}

	return "https"
}

func (u *UnsignedClient) url() (*url.URL, error) {
	return url.Parse(fmt.Sprintf("%s://%s", u.scheme(), u.Domain))
}

type KnotRequest interface {
	Method() string
	Path() string
	Query() url.Values
	Body() []byte
}

func do[T any](u *UnsignedClient, method, path string, query url.Values, body []byte) (*T, error) {
	// Create a copy of the base URL to avoid modifying the original
	base, err := u.url()
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// add path
	base = base.JoinPath(path)

	// add query
	if query != nil {
		base.RawQuery = query.Encode()
	}

	// Create the request
	req, err := http.NewRequest(method, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result T
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (u *UnsignedClient) Index(ownerDid, repoName, ref string) (*types.RepoIndexResponse, error) {
	method := http.MethodGet
	endpoint := path.Join(ownerDid, repoName, "tree", ref)
	if ref == "" {
		endpoint = path.Join(ownerDid, repoName)
	}
	return do[types.RepoIndexResponse](u, method, endpoint, nil, nil)
}

func (u *UnsignedClient) Log(ownerDid, repoName, ref string, page int) (*types.RepoLogResponse, error) {
	method := http.MethodGet
	endpoint := fmt.Sprintf("/%s/%s/log/%s", ownerDid, repoName, url.PathEscape(ref))

	query := url.Values{}
	query.Add("page", strconv.Itoa(page))
	query.Add("per_page", strconv.Itoa(60))

	return do[types.RepoLogResponse](u, method, endpoint, nil, nil)
}

func (u *UnsignedClient) Branches(ownerDid, repoName string) (*types.RepoBranchesResponse, error) {
	method := http.MethodGet
	endpoint := fmt.Sprintf("/%s/%s/branches", ownerDid, repoName)

	return do[types.RepoBranchesResponse](u, method, endpoint, nil, nil)
}

func (u *UnsignedClient) Tags(ownerDid, repoName string) (*types.RepoTagsResponse, error) {
	method := http.MethodGet
	endpoint := fmt.Sprintf("/%s/%s/tags", ownerDid, repoName)

	return do[types.RepoTagsResponse](u, method, endpoint, nil, nil)
}

func (u *UnsignedClient) Branch(ownerDid, repoName, branch string) (*types.RepoBranchResponse, error) {
	method := http.MethodGet
	endpoint := fmt.Sprintf("/%s/%s/branches/%s", ownerDid, repoName, url.PathEscape(branch))

	return do[types.RepoBranchResponse](u, method, endpoint, nil, nil)
}

func (u *UnsignedClient) DefaultBranch(ownerDid, repoName string) (*types.RepoBranchResponse, error) {
	method := http.MethodGet
	endpoint := fmt.Sprintf("/%s/%s/branches/default", ownerDid, repoName)

	return do[types.RepoBranchResponse](u, method, endpoint, nil, nil)
}

func (u *UnsignedClient) Capabilities() (*types.Capabilities, error) {
	method := http.MethodGet
	endpoint := "capabilities"

	return do[types.Capabilities](u, method, endpoint, nil, nil)
}

func (u *UnsignedClient) Compare(ownerDid, repoName, rev1, rev2 string) (*types.RepoFormatPatchResponse, error) {
	method := http.MethodGet
	endpoint := fmt.Sprintf("/%s/%s/compare/%s/%s", ownerDid, repoName, url.PathEscape(rev1), url.PathEscape(rev2))

	return do[types.RepoFormatPatchResponse](u, method, endpoint, nil, nil)
}
