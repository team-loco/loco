package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	oAuth "github.com/team-loco/loco/gen/go/loco/oauth/v1"
	"github.com/team-loco/loco/gen/go/loco/oauth/v1/oauthv1connect"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/keychain"
)

var ErrLoginRequired = errors.New("login required - please run 'loco login'")

const refreshWindow = 5 * time.Minute

// GetCurrentLocoToken retrieves the token for the current OS user.
// If the access token is near expiry and a refresh token is stored, it
// attempts a silent refresh before returning.
func GetCurrentLocoToken(cmd *cobra.Command) (*keychain.UserToken, error) {
	host, err := GetHost(cmd)
	if err != nil {
		return nil, err
	}
	store, err := keychain.ForCurrentUser()
	if err != nil {
		return nil, err
	}
	ctx := cmd.Context()
	return FreshToken(ctx, host, store)
}

func FreshToken(ctx context.Context, host string, store keychain.TokenStore) (*keychain.UserToken, error) {
	locoToken, err := store.Get()
	if errors.Is(err, keychain.ErrNotFound) {
		return nil, ErrLoginRequired
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read token from keychain: %w", err)
	}

	refreshBy := time.Now().Add(refreshWindow)
	if !locoToken.ExpiresAt.Before(refreshBy) {
		return locoToken, nil
	}

	slog.Debug("token is expired or will expire soon", "expires_at", locoToken.ExpiresAt)
	if locoToken.RefreshToken == "" {
		return nil, fmt.Errorf("token is expired. Please re-login via `loco login`")
	}
	slog.Debug("attempting silent token refresh")
	refreshed, err := refreshLocoToken(ctx, host, locoToken.RefreshToken, store)
	if err != nil {
		LogRequestID(ctx, err, "token refresh failed")
		return nil, fmt.Errorf("token expired and refresh failed. Please re-login via `loco login`: %w", err)
	}
	return refreshed, nil
}

// refreshLocoToken calls the RefreshToken RPC using the stored refresh token,
// stores the new token pair in the keychain, and returns the updated UserToken.
func refreshLocoToken(
	ctx context.Context,
	host, refreshToken string,
	store keychain.TokenStore,
) (*keychain.UserToken, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpClient := httputil.NewHTTPClient()
	oAuthClient := oauthv1connect.NewOAuthServiceClient(httpClient, host)
	req := connect.NewRequest(&oAuth.RefreshTokenRequest{
		RefreshToken: refreshToken,
	})
	resp, err := oAuthClient.RefreshToken(ctx, req)
	if err != nil {
		return nil, err
	}

	lifetime := time.Duration(resp.Msg.GetExpiresIn())*time.Second - 10*time.Minute
	newToken := &keychain.UserToken{
		Token:        resp.Msg.GetLocoToken(),
		RefreshToken: resp.Msg.GetRefreshToken(),
		ExpiresAt:    time.Now().Add(lifetime),
	}
	if err = store.Set(*newToken); err != nil {
		return nil, err
	}
	slog.Debug("token refreshed and stored in keychain")
	return newToken, nil
}
