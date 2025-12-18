package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	json "github.com/goccy/go-json"

	"connectrpc.com/connect"
	"github.com/loco-team/loco/shared"
	resourcev1 "github.com/loco-team/loco/shared/proto/resource/v1"
	"github.com/loco-team/loco/shared/proto/resource/v1/resourcev1connect"
	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	"github.com/loco-team/loco/shared/proto/deployment/v1/deploymentv1connect"
	orgv1 "github.com/loco-team/loco/shared/proto/org/v1"
	"github.com/loco-team/loco/shared/proto/org/v1/orgv1connect"
	userv1 "github.com/loco-team/loco/shared/proto/user/v1"
	"github.com/loco-team/loco/shared/proto/user/v1/userv1connect"
	workspacev1 "github.com/loco-team/loco/shared/proto/workspace/v1"
	"github.com/loco-team/loco/shared/proto/workspace/v1/workspacev1connect"
)

// todo: is this too bloated? we likely need to fix this.
// this is literally impossible to test.

type Client struct {
	host       string
	token      string
	httpClient *http.Client

	User       userv1connect.UserServiceClient
	Org        orgv1connect.OrgServiceClient
	Workspace  workspacev1connect.WorkspaceServiceClient
	Resource   resourcev1connect.ResourceServiceClient
	Deployment deploymentv1connect.DeploymentServiceClient
}

func NewClient(host, token string) *Client {
	httpClient := shared.NewHTTPClient()

	return &Client{
		host:       host,
		token:      token,
		httpClient: httpClient,
		User:       userv1connect.NewUserServiceClient(httpClient, host),
		Org:        orgv1connect.NewOrgServiceClient(httpClient, host),
		Workspace:  workspacev1connect.NewWorkspaceServiceClient(httpClient, host),
		Resource:   resourcev1connect.NewResourceServiceClient(httpClient, host),
		Deployment: deploymentv1connect.NewDeploymentServiceClient(httpClient, host),
	}
}

func (c *Client) WithAuth(ctx context.Context) context.Context {
	return ctx
}

// logRequestID extracts and logs the X-Loco-Request-Id only if err is not nil
// duplicate of function in cmd/loco/utils.go - will refactor
func logRequestID(ctx context.Context, err error, msg string) {
	if err == nil {
		return
	}

	const requestIDHeaderName = "X-Loco-Request-Id"
	var headerValue string
	var cErr *connect.Error

	if errors.As(err, &cErr) {
		headerValue = cErr.Meta().Get(requestIDHeaderName)
	}

	slog.ErrorContext(ctx, msg, requestIDHeaderName, headerValue, "error", err)
}

func (c *Client) CreateUser(ctx context.Context, externalID, email, avatarURL string) (*userv1.User, error) {
	req := connect.NewRequest(&userv1.CreateUserRequest{
		ExternalId: externalID,
		Email:      email,
		AvatarUrl:  &avatarURL,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.User.CreateUser(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to create user")
		return nil, err
	}

	return resp.Msg.User, nil
}

func (c *Client) GetCurrentUser(ctx context.Context) (*userv1.User, error) {
	req := connect.NewRequest(&userv1.GetCurrentUserRequest{})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.User.GetCurrentUser(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get current user")
		return nil, err
	}

	return resp.Msg.User, nil
}

func (c *Client) GetCurrentUserOrgs(ctx context.Context) ([]*orgv1.Organization, error) {
	req := connect.NewRequest(&orgv1.GetCurrentUserOrgsRequest{})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Org.GetCurrentUserOrgs(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get current user orgs")
		return nil, err
	}

	return resp.Msg.Orgs, nil
}

func (c *Client) GetUserWorkspaces(ctx context.Context) ([]*workspacev1.Workspace, error) {
	req := connect.NewRequest(&workspacev1.GetUserWorkspacesRequest{})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Workspace.GetUserWorkspaces(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get user workspaces")
		return nil, err
	}

	return resp.Msg.Workspaces, nil
}

func (c *Client) CreateApp(ctx context.Context, appType int32, workspaceID int64, name string) (*resourcev1.Resource, error) {
	req := connect.NewRequest(&resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID,
		Name:        name,
		Type:        resourcev1.ResourceType(appType),
	})

	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Resource.CreateResource(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to create app")
		return nil, err
	}

	return resp.Msg.Resource, nil
}

func (c *Client) GetApp(ctx context.Context, appID string) (*resourcev1.Resource, error) {
	appIDInt, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		logRequestID(ctx, err, "failed to parse app ID")
		return nil, fmt.Errorf("invalid app ID: %w", err)
	}

	req := connect.NewRequest(&resourcev1.GetResourceRequest{ResourceId: appIDInt})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Resource.GetResource(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get app")
		return nil, err
	}

	return resp.Msg.Resource, nil
}

func (c *Client) ListApps(ctx context.Context, workspaceID string) ([]*resourcev1.Resource, error) {
	wsID, err := strconv.ParseInt(workspaceID, 10, 64)
	if err != nil {
		logRequestID(ctx, err, "failed to parse workspace ID")
		return nil, fmt.Errorf("invalid workspace ID: %w", err)
	}

	req := connect.NewRequest(&resourcev1.ListResourcesRequest{WorkspaceId: wsID})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Resource.ListResources(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to list apps")
		return nil, err
	}

	return resp.Msg.Resources, nil
}

func (c *Client) GetAppByName(ctx context.Context, workspaceID int64, appName string) (*resourcev1.Resource, error) {
	req := connect.NewRequest(&resourcev1.GetResourceByNameRequest{
		WorkspaceId: workspaceID,
		Name:        appName,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Resource.GetResourceByName(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get app by name")
		return nil, err
	}

	return resp.Msg.Resource, nil
}

func (c *Client) CreateDeployment(ctx context.Context, appID, clusterID, image string, replicas int32, message string, env map[string]string, ports []*deploymentv1.Port, resources *deploymentv1.ResourceSpec) (int64, error) {
	appIDInt, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid app ID: %w", err)
	}

	req := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		ResourceId: appIDInt,
		Spec: &deploymentv1.DeploymentSpec{
			Image:           &image,
			InitialReplicas: &replicas,
			Env:             env,
		},
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Deployment.CreateDeployment(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to create deployment")
		return 0, err
	}

	return resp.Msg.DeploymentId, nil
}

func (c *Client) GetDeployment(ctx context.Context, deploymentID string) (*deploymentv1.Deployment, error) {
	deploymentIDInt, err := strconv.ParseInt(deploymentID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid deployment ID: %w", err)
	}
	req := connect.NewRequest(&deploymentv1.GetDeploymentRequest{DeploymentId: deploymentIDInt})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Deployment.GetDeployment(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get deployment")
		return nil, err
	}

	return resp.Msg.Deployment, nil
}

func (c *Client) StreamDeployment(ctx context.Context, deploymentID string, eventHandler func(*deploymentv1.DeploymentEvent) error) error {
	deploymentIDInt, err := strconv.ParseInt(deploymentID, 10, 64)
	if err != nil {
		return err
	}
	req := connect.NewRequest(&deploymentv1.StreamDeploymentRequest{DeploymentId: deploymentIDInt})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	stream, err := c.Deployment.StreamDeployment(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to stream deployment")
		return err
	}

	for stream.Receive() {
		event := stream.Msg()
		if err := eventHandler(event); err != nil {
			return err
		}
	}

	if err := stream.Err(); err != nil {
		logRequestID(ctx, err, "failed to stream deployment")
		return err
	}

	return nil
}

func (c *Client) DeleteApp(ctx context.Context, appID string) error {
	appIDInt, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid app ID: %w", err)
	}
	req := connect.NewRequest(&resourcev1.DeleteResourceRequest{ResourceId: appIDInt})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	_, err = c.Resource.DeleteResource(ctx, req)
	logRequestID(ctx, err, "failed to delete app")
	return err
}

func (c *Client) ScaleApp(ctx context.Context, appID int64, replicas *int32, cpu, memory *string) (*resourcev1.DeploymentStatus, error) {
	req := connect.NewRequest(&resourcev1.ScaleResourceRequest{
		ResourceId: appID,
		Replicas:   replicas,
		Cpu:        cpu,
		Memory:     memory,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Resource.ScaleResource(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to scale app")
		return nil, err
	}

	return resp.Msg.Deployment, nil
}

func (c *Client) UpdateAppEnv(ctx context.Context, appID int64, env map[string]string) (*resourcev1.DeploymentStatus, error) {
	req := connect.NewRequest(&resourcev1.UpdateResourceEnvRequest{
		ResourceId: appID,
		Env:        env,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Resource.UpdateResourceEnv(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to update app env")
		return nil, err
	}

	return resp.Msg.Deployment, nil
}

func (c *Client) GetAppStatus(ctx context.Context, appID int64) (*resourcev1.GetResourceStatusResponse, error) {
	req := connect.NewRequest(&resourcev1.GetResourceStatusRequest{
		ResourceId: appID,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Resource.GetResourceStatus(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get app status")
		return nil, err
	}

	return resp.Msg, nil
}

func (c *Client) StreamLogs(ctx context.Context, appID int64, limit *int32, follow *bool, logHandler func(*resourcev1.LogEntry) error) error {
	req := connect.NewRequest(&resourcev1.StreamLogsRequest{
		ResourceId: appID,
		Limit:      limit,
		Follow:     follow,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	stream, err := c.Resource.StreamLogs(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to stream logs")
		return err
	}

	for stream.Receive() {
		logEntry := stream.Msg()
		if err := logHandler(logEntry); err != nil {
			return err
		}
	}

	if err := stream.Err(); err != nil {
		logRequestID(ctx, err, "failed to stream logs")
		return err
	}

	return nil
}

func (c *Client) GetEvents(ctx context.Context, appID int64, limit *int32) ([]*resourcev1.Event, error) {
	req := connect.NewRequest(&resourcev1.GetEventsRequest{
		ResourceId: appID,
		Limit:      limit,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Resource.GetEvents(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.Events, nil
}

// APIError represents an HTTP API error
type APIError struct {
	StatusCode int
	Body       string
	RequestID  string
}

func (e *APIError) Error() string {
	if e.StatusCode == 0 {
		return e.Body
	}

	var msg string
	var payload map[string]string
	if err := json.Unmarshal([]byte(e.Body), &payload); err == nil {
		msg = payload["message"]
	}
	if msg == "" {
		msg = e.Body
	}

	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, msg)
}

// Post makes a POST request (for OAuth flow)
func (c *Client) Post(path string, payload any, headers map[string]string) ([]byte, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", c.host+path, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, &APIError{Body: fmt.Sprintf("failed to create request: %v", err)}
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &APIError{Body: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &APIError{Body: fmt.Sprintf("failed to read response: %v", err)}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	return body, nil
}
