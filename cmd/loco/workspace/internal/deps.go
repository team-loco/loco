package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/shared"
	userv1 "github.com/team-loco/loco/shared/proto/loco/user/v1"
	"github.com/team-loco/loco/shared/proto/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/shared/proto/loco/workspace/v1"
	"github.com/team-loco/loco/shared/proto/loco/workspace/v1/workspacev1connect"
)

// WorkspaceDeps contains all injectable dependencies for workspace commands.
type WorkspaceDeps struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// Config operations
	LoadSessionConfig func() (*config.SessionConfig, error)

	// User operations
	WhoAmI func(ctx context.Context, req *connect.Request[userv1.WhoAmIRequest]) (*connect.Response[userv1.WhoAmIResponse], error)

	// Workspace operations
	CreateWorkspace    func(ctx context.Context, req *connect.Request[workspacev1.CreateWorkspaceRequest]) (*connect.Response[workspacev1.CreateWorkspaceResponse], error)
	GetWorkspace       func(ctx context.Context, req *connect.Request[workspacev1.GetWorkspaceRequest]) (*connect.Response[workspacev1.GetWorkspaceResponse], error)
	UpdateWorkspace    func(ctx context.Context, req *connect.Request[workspacev1.UpdateWorkspaceRequest]) (*connect.Response[workspacev1.UpdateWorkspaceResponse], error)
	DeleteWorkspace    func(ctx context.Context, req *connect.Request[workspacev1.DeleteWorkspaceRequest]) (*connect.Response[workspacev1.DeleteWorkspaceResponse], error)
	ListUserWorkspaces func(ctx context.Context, req *connect.Request[workspacev1.ListUserWorkspacesRequest]) (*connect.Response[workspacev1.ListUserWorkspacesResponse], error)
	ListOrgWorkspaces  func(ctx context.Context, req *connect.Request[workspacev1.ListOrgWorkspacesRequest]) (*connect.Response[workspacev1.ListOrgWorkspacesResponse], error)

	// Auth header for requests
	token string
}

// NewWorkspaceDeps creates WorkspaceDeps with real implementations.
func NewWorkspaceDeps(host, token string) *WorkspaceDeps {
	httpClient := shared.NewHTTPClient()
	wsClient := workspacev1connect.NewWorkspaceServiceClient(httpClient, host)
	userClient := userv1connect.NewUserServiceClient(httpClient, host)

	return &WorkspaceDeps{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,

		LoadSessionConfig: config.Load,

		WhoAmI: userClient.WhoAmI,

		CreateWorkspace:    wsClient.CreateWorkspace,
		GetWorkspace:       wsClient.GetWorkspace,
		UpdateWorkspace:    wsClient.UpdateWorkspace,
		DeleteWorkspace:    wsClient.DeleteWorkspace,
		ListUserWorkspaces: wsClient.ListUserWorkspaces,
		ListOrgWorkspaces:  wsClient.ListOrgWorkspaces,

		token: token,
	}
}

// AuthHeader returns the authorization header value.
func (d *WorkspaceDeps) AuthHeader() string {
	return fmt.Sprintf("Bearer %s", d.token)
}

// GetCurrentLocoToken retrieves the token for the current OS user.
func GetCurrentLocoToken() (*keychain.UserToken, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, err
	}
	locoToken, err := keychain.GetLocoToken(usr.Name)
	if err != nil {
		return nil, err
	}

	if locoToken.ExpiresAt.Before(time.Now().Add(5 * time.Minute)) {
		return nil, fmt.Errorf("token is expired or will expire soon. Please re-login via `loco login`")
	}

	return locoToken, nil
}
