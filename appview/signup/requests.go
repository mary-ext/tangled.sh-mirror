package signup

// We have this extra code here for now since the xrpcclient package
// only supports OAuth'd requests; these are unauthenticated or use PDS admin auth.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// makePdsRequest is a helper method to make requests to the PDS service
func (s *Signup) makePdsRequest(method, endpoint string, body interface{}, useAuth bool) (*http.Response, error) {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/xrpc/%s", s.config.Pds.Host, endpoint)
	req, err := http.NewRequest(method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	if useAuth {
		req.SetBasicAuth("admin", s.config.Pds.AdminSecret)
	}

	return http.DefaultClient.Do(req)
}

// handlePdsError processes error responses from the PDS service
func (s *Signup) handlePdsError(resp *http.Response, action string) error {
	var errorResp struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}

	respBody, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(respBody, &errorResp); err == nil && errorResp.Message != "" {
		return fmt.Errorf("Failed to %s: %s - %s.", action, errorResp.Error, errorResp.Message)
	}

	// Fallback if we couldn't parse the error
	return fmt.Errorf("failed to %s, status code: %d", action, resp.StatusCode)
}

func (s *Signup) inviteCodeRequest() (string, error) {
	body := map[string]any{"useCount": 1}

	resp, err := s.makePdsRequest("POST", "com.atproto.server.createInviteCode", body, true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", s.handlePdsError(resp, "create invite code")
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result["code"], nil
}

func (s *Signup) createAccountRequest(username, password, email, code string) (string, error) {
	parsedURL, err := url.Parse(s.config.Pds.Host)
	if err != nil {
		return "", fmt.Errorf("invalid PDS host URL: %w", err)
	}

	pdsDomain := parsedURL.Hostname()

	body := map[string]string{
		"email":      email,
		"handle":     fmt.Sprintf("%s.%s", username, pdsDomain),
		"password":   password,
		"inviteCode": code,
	}

	resp, err := s.makePdsRequest("POST", "com.atproto.server.createAccount", body, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", s.handlePdsError(resp, "create account")
	}

	var result struct {
		DID string `json:"did"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode create account response: %w", err)
	}

	return result.DID, nil
}
