package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/allegro/bigcache/v3"
	"github.com/goccy/go-json"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/providers"
	oAuth "github.com/team-loco/loco/shared/proto/oauth/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

// OAuthStateCache uses bigcache for storing OAuth state tokens
// todo: this is a temporary in-memory solution; we will eventually move to distributed cache
type OAuthStateCache struct {
	cache *bigcache.BigCache
}

func NewOAuthStateCache(ttl time.Duration) (*OAuthStateCache, error) {
	config := bigcache.DefaultConfig(ttl)
	cache, err := bigcache.New(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create bigcache: %w", err)
	}
	return &OAuthStateCache{cache: cache}, nil
}

func (c *OAuthStateCache) StoreState(ctx context.Context, state string) error {
	if err := c.cache.Set(state, []byte("1")); err != nil {
		slog.ErrorContext(ctx, "failed to store oauth state", "error", err)
		return fmt.Errorf("failed to store state: %w", err)
	}
	slog.InfoContext(ctx, "stored oauth state", "state", state)
	return nil
}

func (c *OAuthStateCache) VerifyAndDeleteState(ctx context.Context, state string) error {
	slog.InfoContext(ctx, "looking for state", "state", state)
	_, err := c.cache.Get(state)
	if err == bigcache.ErrEntryNotFound {
		return errors.New("invalid or expired state")
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to verify state", "error", err)
		return fmt.Errorf("failed to verify state: %w", err)
	}

	// delete the state (one-time use)
	if err := c.cache.Delete(state); err != nil {
		slog.ErrorContext(ctx, "failed to delete state", "error", err)
		return fmt.Errorf("failed to delete state: %w", err)
	}

	slog.InfoContext(ctx, "verified and deleted oauth state")
	return nil
}

func (c *OAuthStateCache) Close() error {
	return c.cache.Close()
}

type OAuthServer struct {
	db         *pgxpool.Pool
	queries    genDb.Querier
	httpClient *http.Client
	stateCache *OAuthStateCache
	machine    *tvm.VendingMachine
}

// GithubUser is the response structure from GitHub's user endpoint
type GithubUser struct {
	ID     int64  `json:"id"`
	Login  string `json:"login"`
	Email  string `json:"email"`
	Avatar string `json:"avatar_url"`
	Name   string `json:"name"`
}

var OAuthConf = &oauth2.Config{
	ClientID:     os.Getenv("GH_OAUTH_CLIENT_ID"),
	ClientSecret: os.Getenv("GH_OAUTH_CLIENT_SECRET"),
	Scopes:       []string{"read:user user:email"},
	Endpoint:     github.Endpoint,
}

var (
	OAuthTokenTTL = time.Duration(8 * time.Hour)
	OAuthStateTTL = time.Duration(10 * time.Minute)
)

func generateSecureRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random state: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func NewOAuthServer(db *pgxpool.Pool, queries genDb.Querier, httpClient *http.Client, machine *tvm.VendingMachine) (*OAuthServer, error) {
	stateCache, err := NewOAuthStateCache(OAuthStateTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to create oauth state cache: %w", err)
	}

	return &OAuthServer{
		db:         db,
		queries:    queries,
		httpClient: httpClient,
		stateCache: stateCache,
		machine:    machine,
	}, nil
}

func (s *OAuthServer) GithubOAuthDetails(
	ctx context.Context, req *connect.Request[oAuth.GithubOAuthDetailsRequest],
) (*connect.Response[oAuth.GithubOAuthDetailsResponse], error) {
	slog.InfoContext(ctx, "Request headers: ", slog.Any("headers", req.Header()))
	res := connect.NewResponse(&oAuth.GithubOAuthDetailsResponse{
		ClientId: OAuthConf.ClientID,
		TokenTtl: OAuthTokenTTL.Seconds(),
	})
	return res, nil
}

// createOrGetUser creates a new user or returns existing user ID
func (s *OAuthServer) createOrGetUser(ctx context.Context, githubUser *GithubUser) (userID int64, isNew bool, err error) {
	user, err := s.queries.GetUserByExternalID(ctx, githubUser.Login)
	if err == nil {
		return user.ID, false, nil
	}

	existingUser, err := s.queries.GetUserByEmail(ctx, githubUser.Email)
	if err == nil {
		return existingUser.ID, false, nil
	}

	externalID := fmt.Sprintf("github:%d", githubUser.ID)
	newUser, err := s.queries.CreateUser(ctx, genDb.CreateUserParams{
		ExternalID: externalID,
		Email:      githubUser.Email,
		AvatarUrl:  pgtype.Text{String: githubUser.Avatar, Valid: githubUser.Avatar != ""},
		Name:       pgtype.Text{String: githubUser.Name, Valid: true},
	})
	if err != nil {
		return 0, false, fmt.Errorf("failed to create user: %w", err)
	}

	slog.InfoContext(ctx, "new user created", "userId", newUser.ID, "github_id", githubUser.ID)
	return newUser.ID, true, nil
}

func (s *OAuthServer) ExchangeGithubToken(
	ctx context.Context,
	req *connect.Request[oAuth.ExchangeGithubTokenRequest],
) (*connect.Response[oAuth.ExchangeGithubTokenResponse], error) {
	githubToken := req.Msg.GithubAccessToken
	if githubToken == "" {
		slog.ErrorContext(ctx, "empty github access token")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("github_access_token is required"))
	}

	// initiate login
	user, token, err := s.machine.Exchange(ctx, providers.Github(githubToken))
	if err != nil {
		slog.ErrorContext(ctx, "exchange github token", "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("exchange token: %w", err))
	}

	res := connect.NewResponse(&oAuth.ExchangeGithubTokenResponse{
		LocoToken: token,
		ExpiresIn: int64(OAuthTokenTTL.Seconds()),
		UserId:    user.ID,
		Name:      user.Name.String,
	})

	slog.InfoContext(ctx, "exchanged github token for loco token", "userId", user.ID)
	return res, nil
}

func isValidGithubUser(hc *http.Client, ctx context.Context, token string) (*GithubUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Add("Accept", "application/vnd.github+json")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		slog.Error(fmt.Sprintf("unexpected status from /user: %d", resp.StatusCode))
		return nil, errors.New("could not confirm identity")
	}

	user := new(GithubUser)
	if err := json.NewDecoder(resp.Body).Decode(user); err != nil {
		return nil, err
	}

	emailsReq, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user/emails", nil)
	if err != nil {
		return nil, err
	}
	emailsReq.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
	emailsReq.Header.Add("Accept", "application/vnd.github+json")

	emailsResp, err := hc.Do(emailsReq)
	if err != nil {
		return nil, err
	}
	defer emailsResp.Body.Close()

	if emailsResp.StatusCode > 299 {
		slog.Error(fmt.Sprintf("unexpected status from /user/emails: %d", emailsResp.StatusCode))
		return nil, errors.New("could not fetch user emails")
	}

	// todo: this can be cleaned up.
	var emailList []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}

	if err := json.NewDecoder(emailsResp.Body).Decode(&emailList); err != nil {
		return nil, err
	}

	for _, e := range emailList {
		if e.Primary && e.Verified {
			user.Email = e.Email
			break
		}
	}

	if user.Email == "" {
		for _, e := range emailList {
			if e.Verified {
				user.Email = e.Email
				break
			}
		}
	}

	return user, nil
}

// GetGithubAuthorizationURL generates the GitHub OAuth authorization URL
func (s *OAuthServer) GetGithubAuthorizationURL(
	ctx context.Context,
	req *connect.Request[oAuth.GetGithubAuthorizationURLRequest],
) (*connect.Response[oAuth.GetGithubAuthorizationURLResponse], error) {
	state := req.Msg.State
	if state == "" {
		var err error
		state, err = generateSecureRandomString(32)
		if err != nil {
			slog.ErrorContext(ctx, "failed to generate state", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate state: %w", err))
		}
	}

	// store state in cache
	if err := s.stateCache.StoreState(ctx, state); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to store state: %w", err))
	}

	// build github oauth url
	authURL := OAuthConf.AuthCodeURL(state, oauth2.AccessTypeOffline)

	res := connect.NewResponse(&oAuth.GetGithubAuthorizationURLResponse{
		AuthorizationUrl: authURL,
		State:            state,
	})

	slog.InfoContext(ctx, "generated github authorization url", "state", state, "authUrl", authURL)
	return res, nil
}

// ExchangeGithubCode exchanges authorization code for Loco token
func (s *OAuthServer) ExchangeGithubCode(
	ctx context.Context,
	req *connect.Request[oAuth.ExchangeGithubCodeRequest],
) (*connect.Response[oAuth.ExchangeGithubCodeResponse], error) {
	code := req.Msg.Code
	state := req.Msg.State

	if code == "" {
		slog.ErrorContext(ctx, "missing authorization code")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("code is required"))
	}

	if state == "" {
		slog.ErrorContext(ctx, "missing state parameter")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("state is required"))
	}

	// verify state
	slog.InfoContext(ctx, "attempting to verify state", "state", state, "code", code)
	if err := s.stateCache.VerifyAndDeleteState(ctx, state); err != nil {
		slog.ErrorContext(ctx, "invalid oauth state", "error", err, "state", state)
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid state parameter"))
	}
	slog.InfoContext(ctx, "state verified and deleted", "state", state)

	// exchange authorization code for github access token
	token, err := OAuthConf.Exchange(ctx, code)
	if err != nil {
		slog.ErrorContext(ctx, "failed to exchange authorization code", "error", err)
		return nil, connect.NewError(
			connect.CodeUnauthenticated,
			fmt.Errorf("failed to exchange code: %w", err),
		)
	}

	user, locoToken, err := s.machine.Exchange(ctx, providers.Github(token.AccessToken))

	res := connect.NewResponse(&oAuth.ExchangeGithubCodeResponse{
		ExpiresIn: int64(OAuthTokenTTL.Seconds()),
		UserId:    user.ID,
		Name:      user.Name.String,
	})

	// set loco token as http-only cookie
	res.Header().Set("Set-Cookie", fmt.Sprintf(
		"loco_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax",
		locoToken,
		int(OAuthTokenTTL.Seconds()),
	))

	slog.InfoContext(ctx, "exchanged github code for loco token", "userId", user.ID, "method", "cookie")
	return res, nil
}
