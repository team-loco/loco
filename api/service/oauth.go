package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/providers"
	oAuth "github.com/team-loco/loco/proto/loco/oauth/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

// OAuthStateCache wraps the cache interface for storing OAuth state tokens
type OAuthStateCache struct {
	cache cache.Cache
}

func NewOAuthStateCache(c cache.Cache) *OAuthStateCache {
	return &OAuthStateCache{cache: c}
}

func (c *OAuthStateCache) StoreState(ctx context.Context, state string) error {
	key := "loco_api:oauth:state:" + state
	if err := c.cache.Set(ctx, key, []byte("1"), OAuthStateTTL); err != nil {
		slog.ErrorContext(ctx, "failed to store oauth state", "error", err)
		return fmt.Errorf("failed to store state: %w", err)
	}
	slog.InfoContext(ctx, "stored oauth state", "state", state)
	return nil
}

func (c *OAuthStateCache) VerifyAndDeleteState(ctx context.Context, state string) error {
	slog.InfoContext(ctx, "looking for state", "state", state)
	key := "loco_api:oauth:state:" + state
	_, err := c.cache.Get(ctx, key)
	if errors.Is(err, cache.ErrNotFound) {
		return errors.New("invalid or expired state")
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to verify state", "error", err)
		return fmt.Errorf("failed to verify state: %w", err)
	}

	// delete the state (one-time use)
	if err := c.cache.Delete(ctx, key); err != nil {
		slog.ErrorContext(ctx, "failed to delete state", "error", err)
		return fmt.Errorf("failed to delete state: %w", err)
	}

	slog.InfoContext(ctx, "verified and deleted oauth state")
	return nil
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
	Scopes:       []string{"read:user", "user:email"},
	Endpoint:     github.Endpoint,
}

var OAuthStateTTL = time.Duration(10 * time.Minute)

// secureFlag returns "; Secure" when running in production so cookies are
// only sent over HTTPS. In other environments it returns an empty string.
func secureFlag() string {
	if os.Getenv("LOCO_ENV") == "production" {
		return "; Secure"
	}
	return ""
}

func generateSecureRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random state: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func NewOAuthServer(db *pgxpool.Pool, queries genDb.Querier, httpClient *http.Client, machine *tvm.VendingMachine, stateCache *OAuthStateCache) *OAuthServer {
	return &OAuthServer{
		db:         db,
		queries:    queries,
		httpClient: httpClient,
		stateCache: stateCache,
		machine:    machine,
	}
}

func (s *OAuthServer) fetchGithubUserData(token string) (*GithubUser, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create github request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Add("Accept", "application/vnd.github+json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch github user data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github user api returned status %d", resp.StatusCode)
	}

	var user GithubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode github user response: %w", err)
	}

	return &user, nil
}

// todo: remove the second we have a proper invitation system.
func (s *OAuthServer) tempCreateUser(ctx context.Context, externalID string, email string, name string, avatarURL string) (*genDb.User, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, fmt.Errorf("database error: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx, ok := s.queries.(*genDb.Queries)
	if !ok {
		slog.ErrorContext(ctx, "failed to cast queries to *genDb.Queries")
		return nil, fmt.Errorf("database error: %w", fmt.Errorf("failed to cast queries"))
	}
	qtx = qtx.WithTx(tx)

	avatarURLPgType := pgtype.Text{String: avatarURL, Valid: avatarURL != ""}
	namePgType := pgtype.Text{String: name, Valid: name != ""}

	user, err := qtx.CreateUser(ctx, genDb.CreateUserParams{
		ExternalID: externalID,
		Email:      email,
		Name:       namePgType,
		AvatarUrl:  avatarURLPgType,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create user", "error", err)
		return nil, fmt.Errorf("database error: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit transaction", "error", err)
		return nil, fmt.Errorf("database error: %w", err)
	}

	if err := s.machine.UpdateRoles(ctx, user.ID.String(), []genDb.EntityScope{
		{EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeAdmin},
	}, []genDb.EntityScope{}); err != nil {
		slog.ErrorContext(ctx, "failed to update user roles", "error", err, "userId", user.ID)
		return nil, fmt.Errorf("database error: %w", err)
	}

	return &user, nil
}

func (s *OAuthServer) GetOAuthDetails(
	ctx context.Context, req *connect.Request[oAuth.GetOAuthDetailsRequest],
) (*connect.Response[oAuth.GetOAuthDetailsResponse], error) {
	// Currently only GitHub is supported
	if req.Msg.GetProvider() != oAuth.OAuthProvider_O_AUTH_PROVIDER_GITHUB {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported oauth provider"))
	}

	res := connect.NewResponse(&oAuth.GetOAuthDetailsResponse{
		ClientId: OAuthConf.ClientID,
		TokenTtl: s.machine.Cfg.SessionAccessTokenDuration.Seconds(),
	})
	return res, nil
}

// todo: fix this function to exchange once.
func (s *OAuthServer) ExchangeOAuthToken(
	ctx context.Context,
	req *connect.Request[oAuth.ExchangeOAuthTokenRequest],
) (*connect.Response[oAuth.ExchangeOAuthTokenResponse], error) {
	// Currently only GitHub is supported
	if req.Msg.GetProvider() != oAuth.OAuthProvider_O_AUTH_PROVIDER_GITHUB {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported oauth provider"))
	}

	token := req.Msg.GetToken()
	if token == "" {
		slog.ErrorContext(ctx, "empty oauth access token")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token is required"))
	}

	ip := req.Header().Get("X-Real-IP")
	if ip == "" {
		ip = req.Header().Get("X-Forwarded-For")
	}
	ua := req.Header().Get("User-Agent")

	user, accessToken, refreshToken, err := s.machine.Exchange(ctx, providers.Github(token), ip, ua)
	if err != nil {
		slog.ErrorContext(ctx, "exchange oauth token", "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("exchange token: %w", err))
	}

	res := connect.NewResponse(&oAuth.ExchangeOAuthTokenResponse{
		LocoToken:    accessToken,
		ExpiresIn:    int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
		UserId:       user.ID.String(),
		Name:         user.Name.String,
		RefreshToken: refreshToken,
	})

	slog.InfoContext(ctx, "exchanged oauth token for loco token", "userId", user.ID.String())
	return res, nil
}

// RefreshToken rotates a session token pair using a refresh token.
// If the request body contains a refresh_token it is used; otherwise the
// loco_refresh_token cookie is read (browser flow).
func (s *OAuthServer) RefreshToken(
	ctx context.Context,
	req *connect.Request[oAuth.RefreshTokenRequest],
) (*connect.Response[oAuth.RefreshTokenResponse], error) {
	refreshToken := req.Msg.GetRefreshToken()
	if refreshToken == "" {
		cookies, err := http.ParseCookie(req.Header().Get("Cookie"))
		if err == nil {
			for _, c := range cookies {
				if c.Name == "loco_refresh_token" {
					refreshToken = c.Value
					break
				}
			}
		}
	}
	if refreshToken == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no refresh token provided"))
	}

	newAccess, newRefresh, err := s.machine.Refresh(ctx, refreshToken)
	if err != nil {
		slog.ErrorContext(ctx, "failed to refresh session token", "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	res := connect.NewResponse(&oAuth.RefreshTokenResponse{
		LocoToken:    newAccess,
		ExpiresIn:    int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
		RefreshToken: newRefresh,
	})
	res.Header().Add("Set-Cookie", fmt.Sprintf(
		"loco_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax%s",
		newAccess, int(s.machine.Cfg.SessionAccessTokenDuration.Seconds()), secureFlag(),
	))
	res.Header().Add("Set-Cookie", fmt.Sprintf(
		"loco_refresh_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax%s",
		newRefresh, int(s.machine.Cfg.SessionRefreshTokenDuration.Seconds()), secureFlag(),
	))

	slog.InfoContext(ctx, "session token refreshed successfully")
	return res, nil
}

// GetOAuthAuthorizationURL generates the OAuth authorization URL for a provider
func (s *OAuthServer) GetOAuthAuthorizationURL(
	ctx context.Context,
	req *connect.Request[oAuth.GetOAuthAuthorizationURLRequest],
) (*connect.Response[oAuth.GetOAuthAuthorizationURLResponse], error) {
	// Currently only GitHub is supported
	if req.Msg.GetProvider() != oAuth.OAuthProvider_O_AUTH_PROVIDER_GITHUB {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported oauth provider"))
	}

	state := req.Msg.GetState()
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

	res := connect.NewResponse(&oAuth.GetOAuthAuthorizationURLResponse{
		AuthorizationUrl: authURL,
		State:            state,
	})

	slog.InfoContext(ctx, "generated oauth authorization url", "provider", req.Msg.GetProvider())
	return res, nil
}

// ExchangeOAuthCode exchanges authorization code for Loco token
func (s *OAuthServer) ExchangeOAuthCode(
	ctx context.Context,
	req *connect.Request[oAuth.ExchangeOAuthCodeRequest],
) (*connect.Response[oAuth.ExchangeOAuthCodeResponse], error) {
	// Currently only GitHub is supported
	if req.Msg.GetProvider() != oAuth.OAuthProvider_O_AUTH_PROVIDER_GITHUB {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported oauth provider"))
	}

	code := req.Msg.GetCode()
	state := req.Msg.GetState()

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

	// get github user data
	emailResp := providers.Github(token.AccessToken)
	address, err := emailResp.Address()
	if err != nil {
		slog.ErrorContext(ctx, "failed to get email from github token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get email: %w", err))
	}

	ip := req.Header().Get("X-Real-IP")
	if ip == "" {
		ip = req.Header().Get("X-Forwarded-For")
	}
	ua := req.Header().Get("User-Agent")

	// try to exchange token for existing user
	user, accessToken, refreshToken, err := s.machine.Exchange(ctx, emailResp, ip, ua)
	if err == tvm.ErrUserNotFound {
		// user doesn't exist, fetch github profile and create user
		githubUser, fetchErr := s.fetchGithubUserData(token.AccessToken)
		if fetchErr != nil {
			slog.ErrorContext(ctx, "failed to fetch github user data", "error", fetchErr)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to fetch github user: %w", fetchErr))
		}

		createdUser, createErr := s.tempCreateUser(ctx, fmt.Sprintf("%d", githubUser.ID), address, githubUser.Name, githubUser.Avatar)
		if createErr != nil {
			slog.ErrorContext(ctx, "failed to create user", "error", createErr)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create user: %w", createErr))
		}

		// exchange again with newly created user
		user, accessToken, refreshToken, err = s.machine.Exchange(ctx, emailResp, ip, ua)
		if err != nil {
			slog.ErrorContext(ctx, "exchange github token for new user", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("exchange token: %w", err))
		}
		slog.InfoContext(ctx, "created new user from github oauth", "userId", createdUser.ID)
	} else if err != nil {
		slog.ErrorContext(ctx, "failed to exchange token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	res := connect.NewResponse(&oAuth.ExchangeOAuthCodeResponse{
		ExpiresIn: int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
		UserId:    user.ID.String(),
		Name:      user.Name.String,
	})

	res.Header().Add("Set-Cookie", fmt.Sprintf(
		"loco_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax%s",
		accessToken,
		int(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
		secureFlag(),
	))
	res.Header().Add("Set-Cookie", fmt.Sprintf(
		"loco_refresh_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax%s",
		refreshToken,
		int(s.machine.Cfg.SessionRefreshTokenDuration.Seconds()),
		secureFlag(),
	))

	slog.InfoContext(ctx, "exchanged oauth code for loco token", "userId", user.ID, "method", "cookie", "provider", req.Msg.GetProvider())
	return res, nil
}
