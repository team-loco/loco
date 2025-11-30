package tvm

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	queries "github.com/loco-team/loco/api/gen/db"
)

// ExchangeGithub exchanges a GitHub token for a TVM token.
func (tvm *VendingMachine) ExchangeGithub(ctx context.Context, githubToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		slog.Error("github token exchange: new request", "error", err)
		return "", ErrGithubExchange
	}
	req.Header.Set("Authorization", "Bearer "+githubToken)
	req.Header.Add("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("github token exchange: do request", "error", err)
		return "", ErrGithubExchange
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("github token exchange: non-200 response", "status_code", resp.StatusCode)
		return "", ErrGithubExchange
	}

	type githubUserResponse struct {
		Email string `json:"email"` // this is the only field we care about (here, at least)
	}
	var guResp githubUserResponse

	err = json.NewDecoder(resp.Body).Decode(&guResp)
	if err != nil {
		slog.Error("github token exchange: decode response", "error", err)
		return "", ErrGithubExchange
	}

	// look up the user by email
	user, err := tvm.queries.GetUserByEmail(ctx, guResp.Email)
	if err != nil {
		slog.Error("github token exchange: get user by email", "email", guResp.Email, "error", err)
		return "", ErrUserNotFound
	}

	// issue a TVM token for the user
	tvmToken, err := tvm.Issue(ctx, user.ID, queries.Entity{
		Type: queries.EntityTypeUser,
		ID:   user.ID,
	}, []queries.EntityScope{
		scopeUserRead.attachEntityID(user.ID),
		scopeUserWrite.attachEntityID(user.ID),
		scopeUserAdmin.attachEntityID(user.ID),
	}, time.Hour*24*7)

	if err != nil {
		slog.Error("github token exchange: issue tvm token", "user_id", user.ID, "error", err)
		return "", ErrIssueToken
	}

	return tvmToken, nil
}
