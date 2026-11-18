package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/team-loco/loco/api/auth"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/tvm"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/bearer"
)

const (
	webLoginTTL     = 5 * time.Minute
	userAgentHeader = "User-Agent"
)

var errInvalidWebLogin = errors.New("invalid dashboard login")

type WebAuth struct {
	Provider *auth.WebProvider
	Resolver *auth.Resolver
	Cookies  auth.WebCookies
}

type webLogin struct {
	Binding    string
	Nonce      string
	Verifier   string
	RedirectTo string
}

func (s *AuthServer) StartWebLogin(
	ctx context.Context,
	req *authv1.StartWebLoginRequest,
) (*authv1.StartWebLoginResponse, error) {
	info, err := s.webLoginCallInfo(ctx)
	if err != nil {
		return nil, err
	}

	path := req.GetRedirectTo()
	if path != "" && !validWebReturnPath(path) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "return path must be relative to the dashboard")
	}
	state := bearer.New("")
	login := webLogin{
		Binding: bearer.New(""), Nonce: bearer.New(""), Verifier: oauth2.GenerateVerifier(), RedirectTo: path,
	}
	destination, err := s.web.Provider.AuthorizationURL(ctx, state, login.Nonce, login.Verifier, req.GetLoginHint())
	if err != nil {
		return nil, webSignInError(err)
	}

	data, err := json.Marshal(login)
	if err != nil {
		return nil, fmt.Errorf("encode dashboard login: %w", err)
	}
	if err := s.cache.Set(ctx, webLoginKey(state), data, webLoginTTL); err != nil {
		return nil, authStoreUnavailable(err)
	}
	s.web.Cookies.Set(
		info.ResponseHeader(),
		auth.WebLoginCookie,
		login.Binding,
		s.now().Add(webLoginTTL),
		http.SameSiteLaxMode,
	)
	return &authv1.StartWebLoginResponse{AuthorizationUrl: destination}, nil
}

func (s *AuthServer) CompleteWebLogin(
	ctx context.Context,
	req *authv1.CompleteWebLoginRequest,
) (*authv1.CompleteWebLoginResponse, error) {
	info, err := s.webLoginCallInfo(ctx)
	if err != nil {
		return nil, err
	}
	callback, err := s.web.Provider.CallbackValues(req.GetCallbackUrl())
	if err != nil {
		return nil, webSignInError(err)
	}
	if len(callback["state"]) != 1 || callback.Get("state") == "" {
		return nil, webSignInError(errInvalidWebLogin)
	}

	binding := s.web.Cookies.Read(info.RequestHeader(), auth.WebLoginCookie)
	login, err := s.takeWebLogin(ctx, callback.Get("state"), binding)
	if err != nil {
		return nil, err
	}
	if len(callback["code"]) != 1 || callback.Get("code") == "" || callback.Has("error") {
		return nil, webSignInError(errInvalidWebLogin)
	}

	id, err := s.web.Provider.Exchange(ctx, callback.Get("code"), login.Verifier, login.Nonce)
	if err != nil {
		return nil, webSignInError(err)
	}
	user, claimed, err := s.web.Resolver.Resolve(ctx, id)
	if err != nil {
		if errors.Is(err, auth.ErrAccountUnavailable) {
			return nil, webSignInError(err)
		}
		if _, rejected := errors.AsType[*auth.SignupRejectedError](err); rejected {
			return nil, webSignInError(err)
		}
		return nil, fmt.Errorf("resolve dashboard identity: %w", err)
	}

	access, refresh, err := s.issueWebSession(ctx, user.ID, id, info)
	if errors.Is(err, tvm.ErrInvalidExpiredToken) {
		return nil, webSignInError(err)
	}
	if err != nil {
		return nil, err
	}
	s.web.Cookies.Set(info.ResponseHeader(), auth.WebLoginCookie, "", time.Time{}, http.SameSiteLaxMode)
	s.setWebSession(info.ResponseHeader(), access, refresh)
	return &authv1.CompleteWebLoginResponse{
		User: dbUserToProto(user), ClaimedOrgs: claimedOrgsProto(claimed), RedirectTo: login.RedirectTo,
	}, nil
}

func (s *AuthServer) RefreshWebSession(
	ctx context.Context,
	_ *authv1.RefreshWebSessionRequest,
) (*authv1.RefreshWebSessionResponse, error) {
	if s.web.Cookies.Origin() == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized)
	}
	info, err := s.webCallInfo(ctx)
	if err != nil {
		return nil, err
	}
	token := s.web.Cookies.Read(info.RequestHeader(), auth.WebRefreshCookie)
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized)
	}
	access, refresh, err := s.refreshSession(ctx, token, genDb.SessionKindWeb)
	if errors.Is(err, tvm.ErrInvalidExpiredToken) {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	if err != nil {
		return nil, fmt.Errorf("refresh dashboard session: %w", err)
	}

	s.setWebSession(info.ResponseHeader(), access, refresh)
	return &authv1.RefreshWebSessionResponse{}, nil
}

func (s *AuthServer) SignOut(
	ctx context.Context,
	_ *authv1.SignOutRequest,
) (*authv1.SignOutResponse, error) {
	info, err := s.webCallInfo(ctx)
	if err != nil {
		return nil, err
	}
	token := s.web.Cookies.Read(info.RequestHeader(), auth.WebRefreshCookie)
	if token == "" {
		token = s.web.Cookies.Read(info.RequestHeader(), auth.WebSessionCookie)
	}
	if token != "" {
		if err := s.machine.RevokeWebSession(ctx, token); err != nil {
			return nil, fmt.Errorf("revoke dashboard session: %w", err)
		}
	}
	s.setWebSession(info.ResponseHeader(), "", "")
	return &authv1.SignOutResponse{}, nil
}

func (s *AuthServer) webLoginCallInfo(ctx context.Context) (*connect.CallInfo, error) {
	if s.web.Provider == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "dashboard sign-in is not configured")
	}
	return s.webCallInfo(ctx)
}

func (s *AuthServer) webCallInfo(ctx context.Context) (*connect.CallInfo, error) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || !s.web.Cookies.AllowsRequest(ctx, info.RequestHeader()) {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied)
	}
	return info, nil
}

func (s *AuthServer) takeWebLogin(ctx context.Context, state, binding string) (webLogin, error) {
	if binding == "" {
		return webLogin{}, webSignInError(errInvalidWebLogin)
	}
	key := webLoginKey(state)
	data, err := s.cache.Get(ctx, key)
	if errors.Is(err, cache.ErrNotFound) {
		return webLogin{}, webSignInError(errInvalidWebLogin)
	}
	if err != nil {
		return webLogin{}, authStoreUnavailable(err)
	}
	var login webLogin
	if err := json.Unmarshal(data, &login); err != nil {
		return webLogin{}, fmt.Errorf("decode dashboard login: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(login.Binding), []byte(binding)) != 1 {
		return webLogin{}, webSignInError(errInvalidWebLogin)
	}
	// The compare runs before the atomic removal so a request without the
	// binding cookie cannot consume a pending login.
	taken, err := s.cache.Take(ctx, key)
	if err != nil {
		return webLogin{}, authStoreUnavailable(err)
	}
	if !taken {
		return webLogin{}, webSignInError(errInvalidWebLogin)
	}
	return login, nil
}

func (s *AuthServer) issueWebSession(
	ctx context.Context,
	userID uuid.UUID,
	id auth.Identity,
	info *connect.CallInfo,
) (string, string, error) {
	var access, refresh string
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if err := lockSessionAccount(ctx, qtx, userID); err != nil {
			return err
		}
		row, err := qtx.GetIdentity(ctx, genDb.GetIdentityParams{Issuer: id.Issuer, Subject: id.Subject})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && row.UserID != userID {
			return tvm.ErrInvalidExpiredToken
		}
		if err != nil {
			return fmt.Errorf("get dashboard identity: %w", err)
		}
		access, refresh, err = s.machine.WithQueries(qtx).IssueWebSession(
			ctx, userID, row.ID, clientAddress(info), info.RequestHeader().Get(userAgentHeader),
		)
		return err
	})
	return access, refresh, err
}

func (s *AuthServer) setWebSession(headers *connect.Header, access, refresh string) {
	var accessExpires, refreshExpires time.Time
	if access != "" {
		now := s.now()
		accessExpires = now.Add(s.machine.Cfg.SessionAccessTokenDuration)
		refreshExpires = now.Add(s.machine.Cfg.SessionRefreshTokenDuration)
	}
	s.web.Cookies.Set(headers, auth.WebSessionCookie, access, accessExpires, http.SameSiteLaxMode)
	s.web.Cookies.Set(headers, auth.WebRefreshCookie, refresh, refreshExpires, http.SameSiteStrictMode)
}

func webSignInError(err error) error {
	return connect.NewError(connect.CodeUnauthenticated, "Could not sign you in").WithCause(err)
}

func webLoginKey(state string) string {
	return "web:login:" + bearer.Hash(state)
}

func validWebReturnPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") &&
		!strings.ContainsAny(path, "\\\t\r\n")
}

func claimedOrgsProto(claimed []auth.ClaimedOrg) []*userv1.ClaimedOrg {
	orgs := make([]*userv1.ClaimedOrg, 0, len(claimed))
	for _, org := range claimed {
		orgs = append(orgs, &userv1.ClaimedOrg{
			OrgId: org.OrgID.String(), OrgName: org.OrgName, InviterName: org.InviterName,
		})
	}
	return orgs
}
