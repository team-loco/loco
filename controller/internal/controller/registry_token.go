package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	annotationTokenExpiry = "loco.io/token-expiry"
	annotationTokenID     = "loco.io/token-id"
	tokenLifetime         = time.Hour
	tokenRefreshWindow    = 10 * time.Minute
)

// ensureImagePullSecret creates or updates the image pull secret for GitLab registry
func (r *LocoResourceReconciler) ensureImagePullSecret(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) (time.Time, error) {
	namespace := getNamespace(locoRes)
	secretName := getImageSecretName(locoRes)
	key := client.ObjectKey{Namespace: namespace, Name: secretName}

	existing := &corev1.Secret{}
	err := r.Get(ctx, key, existing)
	if err != nil && !apierrors.IsNotFound(err) {
		return time.Time{}, fmt.Errorf("get image pull secret %s/%s: %w", namespace, secretName, err)
	}

	previousTokenID := ""
	if err == nil {
		previousTokenID = existing.Annotations[annotationTokenID]
		expiry, parseErr := time.Parse(time.RFC3339, existing.Annotations[annotationTokenExpiry])
		if parseErr == nil && time.Until(expiry) > tokenRefreshWindow {
			return expiry.Add(-tokenRefreshWindow), nil
		}
	}

	slog.InfoContext(ctx, "issuing registry token", "namespace", namespace, "name", secretName)

	expiry := time.Now().Add(tokenLifetime).UTC().Truncate(time.Second)
	token, err := r.createGitlabDeployToken(ctx, expiry)
	if err != nil {
		return time.Time{}, fmt.Errorf("create gitlab deploy token: %w", err)
	}

	dockerConfig, err := buildDockerConfig(r.gitlabRegistryURL, token.Username, token.Token)
	if err != nil {
		return time.Time{}, err
	}

	labels := managedLabels(locoRes)
	annotations := ownerAnnotations(locoRes)
	annotations[annotationTokenExpiry] = expiry.Format(time.RFC3339)
	annotations[annotationTokenID] = strconv.FormatInt(token.ID, 10)
	data := map[string][]byte{corev1.DockerConfigJsonKey: dockerConfig}
	secret := corev1ac.Secret(secretName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithData(data)

	opts := applyOptions()
	if err := r.Apply(ctx, secret, opts...); err != nil {
		r.revokeGitlabDeployTokenBestEffort(ctx, annotations[annotationTokenID])
		return time.Time{}, fmt.Errorf("apply image pull secret %s/%s: %w", namespace, secretName, err)
	}

	if previousTokenID != "" {
		r.revokeGitlabDeployTokenBestEffort(ctx, previousTokenID)
	}

	return expiry.Add(-tokenRefreshWindow), nil
}

func (r *LocoResourceReconciler) revokeCurrentRegistryToken(ctx context.Context, locoRes *locov1alpha1.Application) {
	namespace := getNamespace(locoRes)
	secretName := getImageSecretName(locoRes)
	key := client.ObjectKey{Namespace: namespace, Name: secretName}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, key, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			slog.WarnContext(ctx, "failed to read image pull secret for token revocation", "error", err)
		}
		return
	}

	tokenID := secret.Annotations[annotationTokenID]
	if tokenID == "" {
		return
	}
	r.revokeGitlabDeployTokenBestEffort(ctx, tokenID)
}

func (r *LocoResourceReconciler) revokeGitlabDeployTokenBestEffort(ctx context.Context, tokenID string) {
	if err := r.revokeGitlabDeployToken(ctx, tokenID); err != nil {
		slog.WarnContext(ctx, "failed to revoke gitlab deploy token", "tokenID", tokenID, "error", err)
	}
}

func (r *LocoResourceReconciler) gitlabDeployTokensURL() string {
	projectID := url.PathEscape(r.gitlabProjectID)
	return fmt.Sprintf("%s/api/v4/projects/%s/deploy_tokens", r.gitlabURL, projectID)
}

func (r *LocoResourceReconciler) createGitlabDeployToken(
	ctx context.Context,
	expiresAt time.Time,
) (*gitlabDeployTokenResponse, error) {
	payload := map[string]any{
		"name":       "loco-read-token",
		"scopes":     []string{"read_registry"},
		"expires_at": expiresAt.Format(time.RFC3339),
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	endpoint := r.gitlabDeployTokensURL()
	body := bytes.NewReader(payloadJSON)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", r.gitlabPAT)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute gitlab api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, gitlabStatusError(resp)
	}

	var tokenResp gitlabDeployTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &tokenResp, nil
}

func (r *LocoResourceReconciler) revokeGitlabDeployToken(ctx context.Context, tokenID string) error {
	tokensURL := r.gitlabDeployTokensURL()
	escapedID := url.PathEscape(tokenID)
	endpoint := fmt.Sprintf("%s/%s", tokensURL, escapedID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("create http request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", r.gitlabPAT)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute gitlab api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return gitlabStatusError(resp)
}

func gitlabStatusError(resp *http.Response) error {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("gitlab api returned status %d: read body: %w", resp.StatusCode, err)
	}
	return fmt.Errorf("gitlab api returned status %d: %s", resp.StatusCode, respBody)
}

// gitlabDeployTokenResponse represents a GitLab deploy token response
type gitlabDeployTokenResponse struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// buildDockerConfig builds a .dockerconfigjson byte array for registry authentication
func buildDockerConfig(registryURL, username, token string) ([]byte, error) {
	authStr := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))

	config := map[string]any{
		"auths": map[string]any{
			registryURL: map[string]string{
				"auth": authStr,
			},
		},
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal docker config: %w", err)
	}

	return configJSON, nil
}
