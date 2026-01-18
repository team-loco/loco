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
	orgv1 "github.com/team-loco/loco/shared/proto/loco/org/v1"
	"github.com/team-loco/loco/shared/proto/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/shared/proto/loco/user/v1"
	"github.com/team-loco/loco/shared/proto/loco/user/v1/userv1connect"
)

// OrgDeps contains all injectable dependencies for org commands.
type OrgDeps struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// Config operations
	LoadSessionConfig func() (*config.SessionConfig, error)

	// User operations
	WhoAmI func(ctx context.Context, req *connect.Request[userv1.WhoAmIRequest]) (*connect.Response[userv1.WhoAmIResponse], error)

	// Org operations
	CreateOrg    func(ctx context.Context, req *connect.Request[orgv1.CreateOrgRequest]) (*connect.Response[orgv1.CreateOrgResponse], error)
	GetOrg       func(ctx context.Context, req *connect.Request[orgv1.GetOrgRequest]) (*connect.Response[orgv1.GetOrgResponse], error)
	UpdateOrg    func(ctx context.Context, req *connect.Request[orgv1.UpdateOrgRequest]) (*connect.Response[orgv1.UpdateOrgResponse], error)
	DeleteOrg    func(ctx context.Context, req *connect.Request[orgv1.DeleteOrgRequest]) (*connect.Response[orgv1.DeleteOrgResponse], error)
	ListUserOrgs func(ctx context.Context, req *connect.Request[orgv1.ListUserOrgsRequest]) (*connect.Response[orgv1.ListUserOrgsResponse], error)
	ListOrgUsers func(ctx context.Context, req *connect.Request[orgv1.ListOrgUsersRequest]) (*connect.Response[orgv1.ListOrgUsersResponse], error)

	// Auth header for requests
	token string
}

// NewOrgDeps creates OrgDeps with real implementations.
func NewOrgDeps(host, token string) *OrgDeps {
	httpClient := shared.NewHTTPClient()
	orgClient := orgv1connect.NewOrgServiceClient(httpClient, host)
	userClient := userv1connect.NewUserServiceClient(httpClient, host)

	return &OrgDeps{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,

		LoadSessionConfig: config.Load,

		WhoAmI: userClient.WhoAmI,

		CreateOrg:    orgClient.CreateOrg,
		GetOrg:       orgClient.GetOrg,
		UpdateOrg:    orgClient.UpdateOrg,
		DeleteOrg:    orgClient.DeleteOrg,
		ListUserOrgs: orgClient.ListUserOrgs,
		ListOrgUsers: orgClient.ListOrgUsers,

		token: token,
	}
}

// AuthHeader returns the authorization header value.
func (d *OrgDeps) AuthHeader() string {
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
