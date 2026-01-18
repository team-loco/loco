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
	tokenv1 "github.com/team-loco/loco/shared/proto/token/v1"
	"github.com/team-loco/loco/shared/proto/token/v1/tokenv1connect"
	userv1 "github.com/team-loco/loco/shared/proto/user/v1"
	"github.com/team-loco/loco/shared/proto/user/v1/userv1connect"
)

// TokenDeps contains all injectable dependencies for token commands.
type TokenDeps struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// Config operations
	LoadSessionConfig func() (*config.SessionConfig, error)

	// Keychain operations (for local token management)
	GetLocoToken func(username string) (*keychain.UserToken, error)
	SetLocoToken func(username string, token keychain.UserToken) error

	// User operations
	WhoAmI func(ctx context.Context, req *connect.Request[userv1.WhoAmIRequest]) (*connect.Response[userv1.WhoAmIResponse], error)

	// Token API operations
	CreateToken func(ctx context.Context, req *connect.Request[tokenv1.CreateTokenRequest]) (*connect.Response[tokenv1.CreateTokenResponse], error)
	ListTokens  func(ctx context.Context, req *connect.Request[tokenv1.ListTokensRequest]) (*connect.Response[tokenv1.ListTokensResponse], error)
	GetToken    func(ctx context.Context, req *connect.Request[tokenv1.GetTokenRequest]) (*connect.Response[tokenv1.GetTokenResponse], error)
	RevokeToken func(ctx context.Context, req *connect.Request[tokenv1.RevokeTokenRequest]) (*connect.Response[tokenv1.RevokeTokenResponse], error)

	// Auth header for requests
	token string
}

// NewTokenDeps creates TokenDeps with real implementations (no API access, for local token ops).
func NewTokenDeps() *TokenDeps {
	return &TokenDeps{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,

		LoadSessionConfig: config.Load,
		GetLocoToken:      keychain.GetLocoToken,
		SetLocoToken:      keychain.SetLocoToken,
	}
}

// NewTokenDepsWithAPI creates TokenDeps with API access for token management.
func NewTokenDepsWithAPI(host, token string) *TokenDeps {
	httpClient := shared.NewHTTPClient()
	tokenClient := tokenv1connect.NewTokenServiceClient(httpClient, host)
	userClient := userv1connect.NewUserServiceClient(httpClient, host)

	return &TokenDeps{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,

		LoadSessionConfig: config.Load,
		GetLocoToken:      keychain.GetLocoToken,
		SetLocoToken:      keychain.SetLocoToken,

		WhoAmI:      userClient.WhoAmI,
		CreateToken: tokenClient.CreateToken,
		ListTokens:  tokenClient.ListTokens,
		GetToken:    tokenClient.GetToken,
		RevokeToken: tokenClient.RevokeToken,

		token: token,
	}
}

// AuthHeader returns the authorization header value.
func (d *TokenDeps) AuthHeader() string {
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
