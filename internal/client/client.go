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
	appv1 "github.com/loco-team/loco/shared/proto/app/v1"
	"github.com/loco-team/loco/shared/proto/app/v1/appv1connect"
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
	App        appv1connect.AppServiceClient
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
		App:        appv1connect.NewAppServiceClient(httpClient, host),
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

func (c *Client) CreateApp(ctx context.Context, appType int32, workspaceID int64, clusterID, name, subdomain, domain string) (*appv1.App, error) {
	req := connect.NewRequest(&appv1.CreateAppRequest{
		WorkspaceId: workspaceID,
		Name:        name,
		Type:        appv1.AppType(appType),
		Subdomain:   subdomain,
		Domain:      &domain,
	})

	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.App.CreateApp(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to create app")
		return nil, err
	}

	return resp.Msg.App, nil
}

func (c *Client) GetApp(ctx context.Context, appID string) (*appv1.App, error) {
	appIDInt, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		logRequestID(ctx, err, "failed to parse app ID")
		return nil, fmt.Errorf("invalid app ID: %w", err)
	}

	req := connect.NewRequest(&appv1.GetAppRequest{AppId: appIDInt})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.App.GetApp(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get app")
		return nil, err
	}

	return resp.Msg.App, nil
}

func (c *Client) ListApps(ctx context.Context, workspaceID string) ([]*appv1.App, error) {
	wsID, err := strconv.ParseInt(workspaceID, 10, 64)
	if err != nil {
		logRequestID(ctx, err, "failed to parse workspace ID")
		return nil, fmt.Errorf("invalid workspace ID: %w", err)
	}

	req := connect.NewRequest(&appv1.ListAppsRequest{WorkspaceId: wsID})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.App.ListApps(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to list apps")
		return nil, err
	}

	return resp.Msg.Apps, nil
}

func (c *Client) GetAppByName(ctx context.Context, workspaceID int64, appName string) (*appv1.App, error) {
	req := connect.NewRequest(&appv1.GetAppByNameRequest{
		WorkspaceId: workspaceID,
		Name:        appName,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.App.GetAppByName(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get app by name")
		return nil, err
	}

	return resp.Msg.App, nil
}

func (c *Client) CreateDeployment(ctx context.Context, appID, clusterID, image string, replicas int32, message string, env map[string]string, ports []*deploymentv1.Port, resources *deploymentv1.ResourceSpec) (*deploymentv1.Deployment, error) {
	appIDInt, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid app ID: %w", err)
	}

	req := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		AppId:     appIDInt,
		Image:     image,
		Replicas:  &replicas,
		Env:       env,
		Ports:     ports,
		Resources: resources,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.Deployment.CreateDeployment(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to create deployment")
		return nil, err
	}

	return resp.Msg.Deployment, nil
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
	req := connect.NewRequest(&appv1.DeleteAppRequest{AppId: appIDInt})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	_, err = c.App.DeleteApp(ctx, req)
	logRequestID(ctx, err, "failed to delete app")
	return err
}

func (c *Client) ScaleApp(ctx context.Context, appID int64, replicas *int32, cpu, memory *string) (*appv1.DeploymentStatus, error) {
	req := connect.NewRequest(&appv1.ScaleAppRequest{
		AppId:    appID,
		Replicas: replicas,
		Cpu:      cpu,
		Memory:   memory,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.App.ScaleApp(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to scale app")
		return nil, err
	}

	return resp.Msg.Deployment, nil
}

func (c *Client) UpdateAppEnv(ctx context.Context, appID int64, env map[string]string) (*appv1.DeploymentStatus, error) {
	req := connect.NewRequest(&appv1.UpdateAppEnvRequest{
		AppId: appID,
		Env:   env,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.App.UpdateAppEnv(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to update app env")
		return nil, err
	}

	return resp.Msg.Deployment, nil
}

func (c *Client) GetAppStatus(ctx context.Context, appID int64) (*appv1.GetAppStatusResponse, error) {
	req := connect.NewRequest(&appv1.GetAppStatusRequest{
		AppId: appID,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.App.GetAppStatus(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get app status")
		return nil, err
	}

	return resp.Msg, nil
}

func (c *Client) StreamLogs(ctx context.Context, appID int64, limit *int32, follow *bool, logHandler func(*appv1.LogEntry) error) error {
	req := connect.NewRequest(&appv1.StreamLogsRequest{
		AppId:  appID,
		Limit:  limit,
		Follow: follow,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	stream, err := c.App.StreamLogs(ctx, req)
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

func (c *Client) GetEvents(ctx context.Context, appID int64, limit *int32) ([]*appv1.Event, error) {
	req := connect.NewRequest(&appv1.GetEventsRequest{
		AppId: appID,
		Limit: limit,
	})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.App.GetEvents(ctx, req)
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
