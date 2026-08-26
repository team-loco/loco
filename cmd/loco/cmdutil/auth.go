package cmdutil

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/user"
	"time"

	"connectrpc.com/connect"
	oAuth "github.com/team-loco/loco/gen/go/loco/oauth/v1"
	"github.com/team-loco/loco/gen/go/loco/oauth/v1/oauthv1connect"
	"github.com/team-loco/loco/internal/keychain"
)

// GetCurrentLocoToken retrieves the token for the current OS user.
// If the access token is near expiry and a refresh token is stored, it
// attempts a silent refresh before returning.
func GetCurrentLocoToken() (*keychain.UserToken, error) {
	usr, err := user.Current()
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return nil, err
	}
	locoToken, err := keychain.GetLocoToken(usr.Name)
	if err != nil {
		slog.Debug("failed to get loco token", "error", err)
		return nil, fmt.Errorf("could not get token from secrets storage. Please login via `loco login`")
	}

	if locoToken.ExpiresAt.Before(time.Now().Add(5 * time.Minute)) {
		slog.Debug("token is expired or will expire soon", "expires_at", locoToken.ExpiresAt)
		if locoToken.RefreshToken == "" {
			return nil, fmt.Errorf("token is expired. Please re-login via `loco login`")
		}
		slog.Debug("attempting silent token refresh")
		refreshed, refreshErr := refreshLocoToken(locoToken.RefreshToken, usr.Name)
		if refreshErr != nil {
			slog.Debug("token refresh failed", "error", refreshErr)
			return nil, fmt.Errorf("token expired and refresh failed. Please re-login via `loco login`")
		}
		return refreshed, nil
	}

	return locoToken, nil
}

// refreshLocoToken calls the RefreshToken RPC using the stored refresh token,
// stores the new token pair in the keychain, and returns the updated UserToken.
func refreshLocoToken(refreshToken, userName string) (*keychain.UserToken, error) {
	// todo: cleanup, we shouldnt be doing this inline here.
	host := os.Getenv("LOCO__HOST")
	if host == "" {
		host = defaultLocoHost
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	oAuthClient := oauthv1connect.NewOAuthServiceClient(&http.Client{}, host)
	resp, err := oAuthClient.RefreshToken(ctx, connect.NewRequest(&oAuth.RefreshTokenRequest{
		RefreshToken: refreshToken,
	}))
	if err != nil {
		return nil, err
	}

	newToken := &keychain.UserToken{
		Token:        resp.Msg.LocoToken,
		RefreshToken: resp.Msg.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(resp.Msg.ExpiresIn)*time.Second - 10*time.Minute),
	}
	if err := keychain.SetLocoToken(userName, *newToken); err != nil {
		return nil, err
	}
	slog.Debug("token refreshed and stored in keychain")
	return newToken, nil
}
