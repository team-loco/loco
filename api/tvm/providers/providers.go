// providers implements various email providers for identifying users.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

var ErrGithubExchange = errors.New("an issue occurred while exchanging the github token")

const GithubIssuer = "https://github.com"

func fetchGithubPrimaryEmail(ctx context.Context, client *http.Client, token string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Add("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("github emails api returned status %d", resp.StatusCode)
	}

	type githubEmail struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	var emails []githubEmail

	err = json.NewDecoder(resp.Body).Decode(&emails)
	if err != nil {
		return "", false, err
	}

	for _, email := range emails {
		if email.Primary {
			return email.Email, email.Verified, nil
		}
	}

	return "", false, errors.New("no primary email found")
}

type EmailResponse struct {
	issuer   string
	subject  string
	address  string
	verified bool
	err      error
}

// not necessary, but we keep it cuz we like it.
func (e EmailResponse) Address() (string, error) {
	return e.address, e.err
}

func (e EmailResponse) Issuer() string {
	return e.issuer
}

func (e EmailResponse) Subject() (string, error) {
	return e.subject, e.err
}

func (e EmailResponse) EmailVerified() bool {
	return e.verified
}

func NewEmailResponse(issuer string, subject string, address string, verified bool, err error) EmailResponse {
	return EmailResponse{issuer: issuer, subject: subject, address: address, verified: verified, err: err}
}

func githubResponse(subject string, address string, verified bool, err error) EmailResponse {
	return NewEmailResponse(GithubIssuer, subject, address, verified, err)
}

// Github fetches the user's email from GitHub using the provided OAuth token.
func Github(ctx context.Context, client *http.Client, token string) EmailResponse {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return githubResponse("", "", false, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Add("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return githubResponse("", "", false, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return githubResponse("", "", false, fmt.Errorf("github user api returned status %d", resp.StatusCode))
	}

	type githubUserResponse struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
	}
	var guResp githubUserResponse

	err = json.NewDecoder(resp.Body).Decode(&guResp)
	if err != nil {
		slog.ErrorContext(ctx, "failed to decode github user response", "err", err)
		return githubResponse("", "", false, err)
	}
	if guResp.ID == 0 {
		slog.ErrorContext(ctx, "github user response does not contain an account id")
		return githubResponse("", "", false, ErrGithubExchange)
	}
	subject := strconv.FormatInt(guResp.ID, 10)
	if guResp.Email != "" {
		return githubResponse(subject, guResp.Email, true, nil)
	}

	// attempt to fallback to github's emails endpoint
	slog.InfoContext(ctx, "github user response does not contain email, fetching from emails endpoint")
	email, verified, err := fetchGithubPrimaryEmail(ctx, client, token)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch primary email from github", "err", err)
		return githubResponse("", "", false, ErrGithubExchange)
	}
	if email == "" {
		slog.ErrorContext(ctx, "github user has no primary email address")
		return githubResponse("", "", false, ErrGithubExchange)
	}
	return githubResponse(subject, email, verified, nil)
}
