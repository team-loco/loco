// providers implements various email providers for identifying users.
package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

var ErrGithubExchange = errors.New("an issue occurred while exchanging the github token")

func fetchGithubPrimaryEmail(token string) (string, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Add("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github emails api returned status %d", resp.StatusCode)
	}

	type githubEmail struct {
		Email   string `json:"email"`
		Primary bool   `json:"primary"`
	}
	var emails []githubEmail

	err = json.NewDecoder(resp.Body).Decode(&emails)
	if err != nil {
		return "", err
	}

	for _, email := range emails {
		if email.Primary {
			return email.Email, nil
		}
	}

	return "", errors.New("no primary email found")
}

type EmailResponse struct {
	externalID string
	address    string
	err        error
}

// not necessary, but we keep it cuz we like it.
func (e EmailResponse) Address() (string, error) {
	return e.address, e.err
}

func (e EmailResponse) ExternalID() (string, error) {
	return e.externalID, e.err
}

func NewEmailResponse(externalID string, address string, err error) EmailResponse {
	return EmailResponse{externalID: externalID, address: address, err: err}
}

func GithubExternalID(id int64) string {
	return "github:" + strconv.FormatInt(id, 10)
}

// Github fetches the user's email from GitHub using the provided OAuth token.
func Github(token string) EmailResponse {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return NewEmailResponse("", "", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Add("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return NewEmailResponse("", "", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return NewEmailResponse("", "", fmt.Errorf("github user api returned status %d", resp.StatusCode))
	}

	type githubUserResponse struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
	}
	var guResp githubUserResponse

	err = json.NewDecoder(resp.Body).Decode(&guResp)
	if err != nil {
		slog.Error("failed to decode github user response", "err", err)
		return NewEmailResponse("", "", err)
	}
	if guResp.ID == 0 {
		slog.Error("github user response does not contain an account id")
		return NewEmailResponse("", "", ErrGithubExchange)
	}
	externalID := GithubExternalID(guResp.ID)
	if guResp.Email != "" {
		return NewEmailResponse(externalID, guResp.Email, nil)
	}

	// attempt to fallback to github's emails endpoint
	slog.Info("github user response does not contain email, fetching from emails endpoint")
	email, err := fetchGithubPrimaryEmail(token)
	if err != nil {
		slog.Error("failed to fetch primary email from github", "err", err)
		return NewEmailResponse("", "", ErrGithubExchange)
	}
	if email == "" {
		slog.Error("github user has no primary email address")
		return NewEmailResponse("", "", ErrGithubExchange)
	}
	return NewEmailResponse(externalID, email, nil)
}
