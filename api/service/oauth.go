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
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/providers"
	oAuth "github.com/team-loco/loco/gen/go/loco/oauth/v1"
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
	slog.DebugContext(ctx, "stored oauth state")
	return nil
}

var (
	errTokenAlreadyExchanged = errors.New("oauth token has already been exchanged")
	errInvalidState          = errors.New("invalid or expired state")
)

// MarkTokenExchanged enforces one-time use for ExchangeOAuthToken. Returns errTokenAlreadyExchanged
// if the GitHub token has already been exchanged within OAuthStateTTL.
func (c *OAuthStateCache) MarkTokenExchanged(ctx context.Context, githubToken string) error {
	key := "loco_api:oauth:token_used:" + hashToken(githubToken)
	stored, err := c.cache.SetIfAbsent(ctx, key, []byte("1"), OAuthStateTTL)
	if err != nil {
		return fmt.Errorf("failed to mark token exchanged: %w", err)
	}
	if !stored {
		return errTokenAlreadyExchanged
	}
	return nil
}

func (c *OAuthStateCache) VerifyAndDeleteState(ctx context.Context, state string) error {
	key := "loco_api:oauth:state:" + state
	existed, err := c.cache.Take(ctx, key)
	if err != nil {
		slog.ErrorContext(ctx, "failed to verify state", "error", err)
		return fmt.Errorf("failed to verify state: %w", err)
	}
	if !existed {
		return errInvalidState
	}
	slog.DebugContext(ctx, "verified and deleted oauth state")
	return nil
}

type OAuthServer struct {
	db            *pgxpool.Pool
	queries       genDb.Querier
	httpClient    *http.Client
	stateCache    *OAuthStateCache
	machine       *tvm.VendingMachine
	secureCookies bool
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

var OAuthStateTTL = 10 * time.Minute

// secureFlag returns "; Secure" when secure is set, so cookies are only sent
// over HTTPS. Otherwise it returns an empty string.
func secureFlag(secure bool) string {
	if secure {
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

func NewOAuthServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	httpClient *http.Client,
	machine *tvm.VendingMachine,
	stateCache *OAuthStateCache,
	secureCookies bool,
) *OAuthServer {
	return &OAuthServer{
		db:            db,
		queries:       queries,
		httpClient:    httpClient,
		stateCache:    stateCache,
		machine:       machine,
		secureCookies: secureCookies,
	}
}

func (s *OAuthServer) fetchGithubUserData(ctx context.Context, token string) (*GithubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
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
func (s *OAuthServer) tempCreateUser(
	ctx context.Context,
	externalID string,
	email string,
	name string,
	avatarURL string,
) (*genDb.User, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, ErrDB
	}
	defer tx.Rollback(ctx)

	qtx, ok := s.queries.(*genDb.Queries)
	if !ok {
		slog.ErrorContext(ctx, "failed to cast queries to *genDb.Queries")
		return nil, errors.New("database error")
	}
	qtx = qtx.WithTx(tx)

	user, err := qtx.CreateUser(ctx, genDb.CreateUserParams{
		ExternalID: externalID,
		Email:      email,
		Name:       &name,
		AvatarUrl:  &avatarURL,
	})
	if err != nil {
		if isPgConstraintViolation(err) {
			slog.WarnContext(ctx, "email already registered to another account", "externalId", externalID)
			return nil, ErrEmailAlreadyRegistered
		}
		slog.ErrorContext(ctx, "failed to create user", "error", err)
		return nil, ErrDB
	}

	// Grant self-scopes in the same transaction so user+scopes are atomic.
	for _, es := range []genDb.AddUserScopeParams{
		{UserID: user.ID, EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeRead},
		{UserID: user.ID, EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeWrite},
		{UserID: user.ID, EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeAdmin},
	} {
		if err := qtx.AddUserScope(ctx, es); err != nil {
			slog.ErrorContext(ctx, "failed to grant user scope", "error", err, "userId", user.ID)
			return nil, ErrDB
		}
	}

	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit transaction", "error", err)
		return nil, ErrDB
	}

	return &user, nil
}

func (s *OAuthServer) exchangeGithubToken(
	ctx context.Context,
	githubToken string,
	ip string,
	userAgent string,
	createIfMissing bool,
) (genDb.User, string, string, error) {
	emailResp := providers.Github(ctx, s.httpClient, githubToken)
	user, accessToken, refreshToken, err := s.machine.Exchange(ctx, emailResp, ip, userAgent)
	if !errors.Is(err, tvm.ErrUserNotFound) || !createIfMissing {
		return user, accessToken, refreshToken, err
	}

	address, err := emailResp.Address()
	if err != nil {
		return genDb.User{}, "", "", fmt.Errorf("failed to get email: %w", err)
	}

	githubUser, err := s.fetchGithubUserData(ctx, githubToken)
	if err != nil {
		return genDb.User{}, "", "", fmt.Errorf("failed to fetch github user: %w", err)
	}

	externalID, err := emailResp.ExternalID()
	if err != nil {
		return genDb.User{}, "", "", fmt.Errorf("failed to get external id: %w", err)
	}

	createdUser, err := s.tempCreateUser(ctx, externalID, address, githubUser.Name, githubUser.Avatar)
	if err != nil {
		return genDb.User{}, "", "", fmt.Errorf("failed to create user: %w", err)
	}
	slog.InfoContext(ctx, "created new user from github oauth", "userId", createdUser.ID)

	return s.machine.Exchange(ctx, emailResp, ip, userAgent)
}

func exchangeError(err error) error {
	if errors.Is(err, tvm.ErrUserNotFound) {
		return connect.NewError(
			connect.CodeUnauthenticated,
			errors.New("no Loco account is linked to this GitHub account"),
		)
	}
	if errors.Is(err, ErrEmailAlreadyRegistered) {
		return connect.NewError(
			connect.CodeAlreadyExists,
			errors.New("the primary email of this GitHub account is already used by another Loco account"),
		)
	}
	if errors.Is(err, tvm.ErrExchange) {
		return connect.NewError(
			connect.CodeUnauthenticated,
			errors.New("could not read the primary email address of your GitHub account"),
		)
	}
	return connect.NewError(connect.CodeInternal, errors.New("could not complete sign-in"))
}

func (s *OAuthServer) GetOAuthDetails(
	_ context.Context, req *connect.Request[oAuth.GetOAuthDetailsRequest],
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

	// Enforce one-time use: reject if this GitHub token has already been exchanged.
	markErr := s.stateCache.MarkTokenExchanged(ctx, token)
	if errors.Is(markErr, errTokenAlreadyExchanged) {
		slog.WarnContext(ctx, "oauth token already exchanged")
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("this GitHub sign-in has already been used, start a new one"),
		)
	}
	if markErr != nil {
		slog.ErrorContext(ctx, "failed to record oauth token exchange", "error", markErr)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("sign-in is temporarily unavailable"))
	}

	ip := req.Header().Get("X-Real-IP")
	if ip == "" {
		ip = req.Header().Get("X-Forwarded-For")
	}
	ua := req.Header().Get("User-Agent")

	user, accessToken, refreshToken, err := s.exchangeGithubToken(
		ctx,
		token,
		ip,
		ua,
		req.Msg.GetCreateUserIfNotExists(),
	)
	if err != nil {
		slog.ErrorContext(ctx, "exchange oauth token", "error", err)
		return nil, exchangeError(err)
	}

	res := connect.NewResponse(&oAuth.ExchangeOAuthTokenResponse{
		LocoToken:    accessToken,
		ExpiresIn:    int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
		UserId:       user.ID.String(),
		Name:         derefString(user.Name),
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
	if errors.Is(err, tvm.ErrInvalidExpiredToken) {
		slog.WarnContext(ctx, "failed to refresh session token", "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("your session has expired, sign in again"))
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to refresh session token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("could not refresh your session"))
	}

	res := connect.NewResponse(&oAuth.RefreshTokenResponse{
		LocoToken:    newAccess,
		ExpiresIn:    int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
		RefreshToken: newRefresh,
	})
	res.Header().Add("Set-Cookie", fmt.Sprintf(
		"loco_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax%s",
		newAccess, int(s.machine.Cfg.SessionAccessTokenDuration.Seconds()), secureFlag(s.secureCookies),
	))
	res.Header().Add("Set-Cookie", fmt.Sprintf(
		"loco_refresh_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax%s",
		newRefresh, int(s.machine.Cfg.SessionRefreshTokenDuration.Seconds()), secureFlag(s.secureCookies),
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
			return nil, connect.NewError(connect.CodeInternal, errors.New("could not start sign-in"))
		}
	}

	// store state in cache
	if err := s.stateCache.StoreState(ctx, state); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("could not start sign-in"))
	}
	slog.InfoContext(ctx, "stored state in cache successfully")

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
	verifyErr := s.stateCache.VerifyAndDeleteState(ctx, state)
	if errors.Is(verifyErr, errInvalidState) {
		slog.WarnContext(ctx, "invalid oauth state")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid state parameter"))
	}
	if verifyErr != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("sign-in is temporarily unavailable"))
	}

	// exchange authorization code for github access token
	token, err := OAuthConf.Exchange(ctx, code)
	if err != nil {
		slog.ErrorContext(ctx, "failed to exchange authorization code", "error", err)
		return nil, connect.NewError(
			connect.CodeUnauthenticated,
			errors.New("GitHub did not accept the sign-in, start a new one"),
		)
	}

	ip := req.Header().Get("X-Real-IP")
	if ip == "" {
		ip = req.Header().Get("X-Forwarded-For")
	}
	ua := req.Header().Get("User-Agent")

	user, accessToken, refreshToken, err := s.exchangeGithubToken(ctx, token.AccessToken, ip, ua, true)
	if err != nil {
		slog.ErrorContext(ctx, "failed to exchange token", "error", err)
		return nil, exchangeError(err)
	}

	res := connect.NewResponse(&oAuth.ExchangeOAuthCodeResponse{
		ExpiresIn: int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
		UserId:    user.ID.String(),
	})

	res.Header().Add("Set-Cookie", fmt.Sprintf(
		"loco_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax%s",
		accessToken,
		int(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
		secureFlag(s.secureCookies),
	))
	res.Header().Add("Set-Cookie", fmt.Sprintf(
		"loco_refresh_token=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax%s",
		refreshToken,
		int(s.machine.Cfg.SessionRefreshTokenDuration.Seconds()),
		secureFlag(s.secureCookies),
	))

	slog.InfoContext(
		ctx,
		"exchanged oauth code for loco token",
		"userId",
		user.ID,
		"method",
		"cookie",
		"provider",
		req.Msg.GetProvider(),
	)
	return res, nil
}
