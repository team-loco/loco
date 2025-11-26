package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/goccy/go-json"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/jwtutil"
	oAuth "github.com/loco-team/loco/shared/proto/oauth/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

type OAuthServer struct {
	db         *pgxpool.Pool
	queries    *genDb.Queries
	httpClient *http.Client
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
	RedirectURL:  os.Getenv("GH_OAUTH_REDIRECT_URL"),
}

var OAuthTokenTTL = time.Duration(8 * time.Hour)

func NewOAuthServer(db *pgxpool.Pool, queries *genDb.Queries, httpClient *http.Client) *OAuthServer {
	return &OAuthServer{db: db, queries: queries, httpClient: httpClient}
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

	// todo: only create user if they don't already exist.
	// todo: ctx should be first param
	githubUser, err := isValidGithubUser(s.httpClient, ctx, req.Msg.GetGithubAccessToken())
	if err != nil {
		slog.ErrorContext(ctx, "failed to validate github token", "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid github token: %w", err))
	}

	// todo: should we be creating user here?
	userID, _, err := s.createOrGetUser(ctx, githubUser)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create/get user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to set up user: %w", err))
	}

	externalUsername := "github:" + githubUser.Login
	locoJWT, err := jwtutil.GenerateLocoJWT(userID, githubUser.Login, externalUsername, OAuthTokenTTL)
	if err != nil {
		slog.ErrorContext(ctx, "failed to generate loco jwt", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate token: %w", err))
	}

	res := connect.NewResponse(&oAuth.ExchangeGithubTokenResponse{
		LocoToken: locoJWT,
		ExpiresIn: int64(OAuthTokenTTL.Seconds()),
		UserId:    userID,
		Username:  githubUser.Login,
	})

	slog.InfoContext(ctx, "exchanged github token for loco token", "userId", userID)
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
