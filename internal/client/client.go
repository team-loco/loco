package client

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/team-loco/loco/internal/rpc"

	"connectrpc.com/connect/v2"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

type Client struct {
	User       userv1connect.UserServiceClient
	Org        orgv1connect.OrgServiceClient
	Workspace  workspacev1connect.WorkspaceServiceClient
	Resource   resourcev1connect.ResourceServiceClient
	Deployment deploymentv1connect.DeploymentServiceClient
}

func NewClient(host, token string) *Client {
	clients := rpc.ForHost(host, token)
	return &Client{
		User:       clients.User,
		Org:        clients.Org,
		Workspace:  clients.Workspace,
		Resource:   clients.Resource,
		Deployment: clients.Deployment,
	}
}

// logRequestID extracts and logs the X-Loco-Request-Id only if err is not nil
// duplicate of function in cmd/loco/utils.go - will refactor
func logRequestID(ctx context.Context, err error, msg string) {
	if err == nil {
		return
	}

	const requestIDHeaderName = "X-Loco-Request-Id"
	var headerValue string

	if info, ok := connect.CallInfoForClientContext(ctx); ok {
		headerValue = info.ResponseHeader().Get(requestIDHeaderName)
	}

	slog.ErrorContext(ctx, msg, requestIDHeaderName, headerValue, "error", err)
}

func (c *Client) GetCurrentUser(ctx context.Context) (*userv1.User, error) {
	req := &userv1.WhoAmIRequest{}

	resp, err := c.User.WhoAmI(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get current user")
		return nil, err
	}

	return resp.GetUser(), nil
}

func (c *Client) GetCurrentUserOrgs(ctx context.Context, userID string) ([]*orgv1.Organization, error) {
	req := &orgv1.ListUserOrgsRequest{
		UserId:   userID,
		PageSize: 100,
	}

	resp, err := c.Org.ListUserOrgs(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get user orgs")
		return nil, err
	}

	return resp.GetOrgs(), nil
}

func (c *Client) GetUserWorkspaces(ctx context.Context, userID string) ([]*workspacev1.Workspace, error) {
	req := &workspacev1.ListUserWorkspacesRequest{
		UserId:   userID,
		PageSize: 100,
	}

	resp, err := c.Workspace.ListUserWorkspaces(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get user workspaces")
		return nil, err
	}

	return resp.GetWorkspaces(), nil
}

func (c *Client) GetApp(ctx context.Context, appID string) (*resourcev1.Resource, error) {
	req := &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: appID},
	}

	resp, err := c.Resource.GetResource(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get app")
		return nil, err
	}

	return resp.GetResource(), nil
}

func (c *Client) ListApps(ctx context.Context, workspaceID string) ([]*resourcev1.Resource, error) {
	req := &resourcev1.ListWorkspaceResourcesRequest{WorkspaceId: workspaceID}

	resp, err := c.Resource.ListWorkspaceResources(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to list apps")
		return nil, err
	}

	return resp.GetResources(), nil
}

func (c *Client) GetAppByName(ctx context.Context, workspaceID string, appName string) (*resourcev1.Resource, error) {
	req := &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{
			NameKey: &resourcev1.GetResourceNameKey{
				WorkspaceId: workspaceID,
				Name:        appName,
			},
		},
	}

	resp, err := c.Resource.GetResource(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get app by name")
		return nil, err
	}

	return resp.GetResource(), nil
}

func (c *Client) GetDeployment(ctx context.Context, deploymentID string) (*deploymentv1.Deployment, error) {
	req := &deploymentv1.GetDeploymentRequest{DeploymentId: deploymentID}

	resp, err := c.Deployment.GetDeployment(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to get deployment")
		return nil, err
	}

	return resp.GetDeployment(), nil
}

func (c *Client) StreamDeployment(
	ctx context.Context,
	deploymentID string,
	eventHandler func(*deploymentv1.WatchDeploymentResponse) error,
) error {
	req := &deploymentv1.WatchDeploymentRequest{DeploymentId: deploymentID}

	stream, err := c.Deployment.WatchDeployment(ctx, req)
	if err != nil {
		logRequestID(ctx, err, "failed to stream deployment")
		return err
	}

	for {
		msg, err2 := stream.Receive()
		if err2 != nil {
			if errors.Is(err2, io.EOF) {
				break
			}
			logRequestID(ctx, err2, "failed to stream deployment")
			return err2
		}
		if err := eventHandler(msg); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) DeleteApp(ctx context.Context, appID string) error {
	req := &resourcev1.DeleteResourceRequest{ResourceId: appID}

	_, err := c.Resource.DeleteResource(ctx, req)
	logRequestID(ctx, err, "failed to delete app")
	return err
}

func (c *Client) GetEvents(ctx context.Context, appID string, limit *int32) ([]*resourcev1.Event, error) {
	req := &resourcev1.ListResourceEventsRequest{
		ResourceId: appID,
		Limit:      limit,
	}

	resp, err := c.Resource.ListResourceEvents(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.GetEvents(), nil
}
