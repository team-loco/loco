package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/team-loco/loco/cmd/internal/host"
	"github.com/team-loco/loco/internal/rpc"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
)

var (
	errTokenExpired  = errors.New("token is expired. Please re-login via `loco login`")
	ErrLoginRequired = errors.New("login required - please run 'loco login'")
)

const refreshWindow = 5 * time.Minute

// GetCurrentLocoToken retrieves the token for the current OS user.
// If the access token is near expiry and a refresh token is stored, it
// attempts a silent refresh before returning.
func GetCurrentLocoToken(cmd *cobra.Command) (*keychain.UserToken, error) {
	apiHost, err := host.GetHost(cmd)
	if err != nil {
		return nil, err
	}
	store, err := keychain.ForCurrentUser()
	if err != nil {
		return nil, err
	}
	ctx := cmd.Context()
	return FreshToken(ctx, apiHost, store)
}

// Login holds the clients for an authenticated command.
type Login struct {
	Clients  *rpc.Clients
	APIToken bool
}

// ResolveLogin resolves the host and the logged-in token.
func ResolveLogin(cmd *cobra.Command) (Login, error) {
	apiHost, err := host.GetHost(cmd)
	if err != nil {
		return Login{}, err
	}
	locoToken, err := GetCurrentLocoToken(cmd)
	if err != nil {
		return Login{}, err
	}
	return Login{Clients: rpc.ForHost(apiHost, locoToken.Token)}, nil
}

func FreshToken(ctx context.Context, apiHost string, store keychain.TokenStore) (*keychain.UserToken, error) {
	locoToken, err := store.Get()
	if errors.Is(err, keychain.ErrNotFound) {
		return nil, ErrLoginRequired
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read token from keychain: %w", err)
	}
	if locoToken.Host == "" {
		return nil, ErrLoginRequired
	}
	if locoToken.Host != apiHost {
		return nil, fmt.Errorf(
			"logged in to %s, not %s - run 'loco login --host %s' to switch",
			locoToken.Host,
			apiHost,
			apiHost,
		)
	}

	refreshBy := time.Now().Add(refreshWindow)
	if !locoToken.ExpiresAt.Before(refreshBy) {
		return locoToken, nil
	}

	slog.Debug("token is expired or will expire soon", "expires_at", locoToken.ExpiresAt)
	if locoToken.RefreshToken == "" {
		return nil, errTokenExpired
	}
	slog.Debug("attempting silent token refresh")
	refreshed, err := refreshLocoToken(ctx, apiHost, locoToken.RefreshToken, store)
	if err != nil {
		return nil, fmt.Errorf("token expired and refresh failed. Please re-login via `loco login`: %w", err)
	}
	return refreshed, nil
}

// refreshLocoToken calls the RefreshCLIToken RPC using the stored refresh token,
// stores the new token pair in the keychain, and returns the updated UserToken.
func refreshLocoToken(
	ctx context.Context,
	apiHost, refreshToken string,
	store keychain.TokenStore,
) (*keychain.UserToken, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpClient := rpc.NewHTTPClient()
	authClients := rpc.NewHTTPClients(httpClient, apiHost, "")
	authClient := authClients.Auth
	req := &authv1.RefreshCLITokenRequest{
		RefreshToken: refreshToken,
	}
	resp, err := authClient.RefreshCLIToken(ctx, req)
	if err != nil {
		return nil, err
	}

	newToken := TokenFromCLITokens(apiHost, resp.GetTokens())
	if err = store.Set(*newToken); err != nil {
		return nil, err
	}
	slog.Debug("token refreshed and stored in keychain")
	return newToken, nil
}

func TokenFromCLITokens(apiHost string, tokens *authv1.CLITokens) *keychain.UserToken {
	lifetime := time.Duration(tokens.GetExpiresIn())*time.Second - 10*time.Minute
	return &keychain.UserToken{
		Host:         apiHost,
		Token:        tokens.GetAccessToken(),
		RefreshToken: tokens.GetRefreshToken(),
		ExpiresAt:    time.Now().Add(lifetime),
	}
}
