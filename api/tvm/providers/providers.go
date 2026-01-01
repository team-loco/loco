// providers implements various email providers for identifying users.
package providers

import (
	"encoding/json"
	"errors"
	"net/http"
)

var (
	ErrGithubExchange = errors.New("an issue occured while exchanging the github token")
)

type EmailResponse struct {
	address string
	err     error
}

func (e EmailResponse) Address() (string, error) {
	return e.address, e.err
}

func NewEmailResponse(address string, err error) EmailResponse {
	return EmailResponse{address: address, err: err}
}

// Github fetches the user's email from GitHub using the provided OAuth token.
func Github(token string) EmailResponse {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return NewEmailResponse("", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Add("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return NewEmailResponse("", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return NewEmailResponse("", err)
	}

	type githubUserResponse struct {
		Email string `json:"email"` // this is the only field we care about (here, at least)
	}
	var guResp githubUserResponse

	err = json.NewDecoder(resp.Body).Decode(&guResp)
	if err != nil {
		return NewEmailResponse("", err)
	}

	return NewEmailResponse(guResp.Email, nil)
}
