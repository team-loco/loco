package tvm

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	queries "github.com/team-loco/loco/api/gen/db"
)

// machine.VerifyWithIdentity(ctx, token, action.New(action.ReadWorkspace, workspaceID)) -> (err error)
// tvm.Exchange(ctx, tvm.Github, githubToken) -> (token string, err error)

// EmailProvider returns the email associated with an external provider (e.g. email from github token).
type EmailProvider func(ctx context.Context, token string) (string, error)

func Github(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		slog.Error("github token exchange: new request", "error", err)
		return "", ErrGithubExchange
	}
	req.Header.Set("Authorization", "Bearer "+token)
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

	return guResp.Email, nil
}

func (tvm *VendingMachine) Exchange(ctx context.Context, emailProvider EmailProvider, token string) (string, error) {
	email, err := emailProvider(ctx, token)
	if err != nil {
		return "", err
	}

	// look up the user by email
	userScopes, err := tvm.queries.GetUserScopesByEmail(ctx, email)
	if err != nil {
		slog.Error("github token exchange: get user by email", "email", email, "error", err)
		return "", ErrUserNotFound
	}
	if len(userScopes) == 0 { // either user not found or has no scopes
		slog.Error("github token exchange: user not found or has no scopes", "email", email)
		return "", ErrUserNotFound
	}
	userID := userScopes[0].UserID

	// issue the token
	return tvm.issueNoCheck(ctx, queries.Entity{
		Type: queries.EntityTypeUser,
		ID:   userID,
	}, queries.EntityScopesFromUserScopes(userScopes), tvm.cfg.LoginTokenDuration)
}
