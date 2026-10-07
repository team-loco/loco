package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// GitlabDeployTokenResponse represents a successful response from GitLab's POST /projects/deploy_tokens API
type GitlabDeployTokenResponse struct {
	Username  string   `json:"username"`
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expires_at"`
	Revoked   bool     `json:"revoked"`
	Expired   bool     `json:"expired"`
	Scopes    []string `json:"scopes"`
}

// GitlabClient handles interactions with GitLab API
type GitlabClient struct {
	baseURL string
	client  *http.Client
}

// NewGitlabClient creates a new GitLab API client
func NewGitlabClient(baseURL string, httpClient *http.Client) *GitlabClient {
	return &GitlabClient{
		baseURL: baseURL,
		client:  httpClient,
	}
}

// CreateDeployToken generates a short-lived Gitlab CR deploy token.
func (c *GitlabClient) CreateDeployToken(
	ctx context.Context,
	personalAccessToken string,
	projectID string,
	payload map[string]any,
) (*GitlabDeployTokenResponse, error) {
	deployTokenPath := fmt.Sprintf("%s/api/v4/projects/%s/deploy_tokens", c.baseURL, projectID)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deployTokenPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Private-Token", personalAccessToken)

	resp, err := c.client.Do(req)
	if err != nil {
		slog.ErrorContext(ctx, "failed to execute gitlab api request", slog.String("error", err.Error()))
		return nil, fmt.Errorf("failed to create deploy token: %w", err)
	}
	defer resp.Body.Close()

	// todo: improve error-handling here.
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.ErrorContext(ctx, err.Error())
			return nil, err
		}

		slog.ErrorContext(ctx, "unexpected status from gitlab api",
			slog.Int("status_code", resp.StatusCode),
			slog.String("response", string(respBody)),
		)
		return nil, fmt.Errorf("gitlab api returned status %d", resp.StatusCode)
	}

	var tokenResp GitlabDeployTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &tokenResp, nil
}
