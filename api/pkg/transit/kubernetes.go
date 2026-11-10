package transit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	authPathPrefix = "auth"
	loginOperation = "login"
)

var ErrLoginNoToken = errors.New("transit kubernetes login returned no token")

type loginRequest struct {
	Role string `json:"role"`
	JWT  string `json:"jwt"`
}

type loginResponse struct {
	Auth struct {
		ClientToken   string `json:"client_token"`
		LeaseDuration int64  `json:"lease_duration"`
	} `json:"auth"`
}

func (t *Client) login(ctx context.Context) (tokenLease, error) {
	jwt, err := os.ReadFile(t.kubernetes.TokenFile) //nolint:gosec
	if err != nil {
		return tokenLease{}, fmt.Errorf("read the kubernetes service account token: %w", err)
	}
	request := loginRequest{Role: t.kubernetes.Role, JWT: strings.TrimSpace(string(jwt))}
	var response loginResponse
	path := strings.Join([]string{authPathPrefix, t.kubernetes.Mount, loginOperation}, pathSep)
	if err := t.send(ctx, http.MethodPost, path, "", request, &response); err != nil {
		return tokenLease{}, fmt.Errorf("transit kubernetes login as %s: %w", t.kubernetes.Role, err)
	}
	if response.Auth.ClientToken == "" {
		return tokenLease{}, ErrLoginNoToken
	}
	ttl := time.Duration(response.Auth.LeaseDuration) * time.Second
	return tokenLease{token: response.Auth.ClientToken, ttl: ttl}, nil
}
