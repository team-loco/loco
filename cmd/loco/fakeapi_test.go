package loco

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"

	"connectrpc.com/connect"
	oauthv1 "github.com/team-loco/loco/gen/go/loco/oauth/v1"
	"github.com/team-loco/loco/gen/go/loco/oauth/v1/oauthv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
)

const (
	fakeAPIToken          = "test-token"
	fakeAPIRefreshToken   = "test-refresh-token"
	fakeAPIRefreshedToken = "refreshed-token"
)

type apiCall struct {
	method        string
	authorization string
}

type fakeAPI struct {
	userv1connect.UnimplementedUserServiceHandler

	mu       sync.Mutex
	users    map[string]*userv1.User
	failures map[string]connect.Code
	calls    []apiCall
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		users: map[string]*userv1.User{
			fakeAPIToken: {
				Id:         "1",
				ExternalId: "gh-1",
				Email:      "test@loco.build",
				Name:       "Test User",
			},
		},
		failures: map[string]connect.Code{},
	}
}

func (f *fakeAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(userv1connect.NewUserServiceHandler(f))
	mux.Handle(oauthv1connect.NewOAuthServiceHandler(&fakeOAuthService{api: f}))
	return mux
}

func (f *fakeAPI) fail(method string, code connect.Code) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = code
}

func (f *fakeAPI) called(method, authorization string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.method == method && (authorization == "" || c.authorization == authorization) {
			return true
		}
	}
	return false
}

func (f *fakeAPI) record(spec connect.Spec, authorization string) error {
	method := path.Base(spec.Procedure)
	f.calls = append(f.calls, apiCall{method: method, authorization: authorization})
	if code, ok := f.failures[method]; ok {
		return connect.NewError(code, fmt.Errorf("fakeapi: %s configured to fail", method))
	}
	return nil
}

func (f *fakeAPI) authenticate(spec connect.Spec, header http.Header) (string, *userv1.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	authorization := header.Get("Authorization")
	if err := f.record(spec, authorization); err != nil {
		return "", nil, err
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	usr, ok := f.users[token]
	if !ok {
		return "", nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("fakeapi: unknown token"))
	}
	return token, usr, nil
}

func (f *fakeAPI) WhoAmI(
	_ context.Context,
	req *connect.Request[userv1.WhoAmIRequest],
) (*connect.Response[userv1.WhoAmIResponse], error) {
	_, usr, err := f.authenticate(req.Spec(), req.Header())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&userv1.WhoAmIResponse{User: usr}), nil
}

func (f *fakeAPI) Logout(
	_ context.Context,
	req *connect.Request[userv1.LogoutRequest],
) (*connect.Response[userv1.LogoutResponse], error) {
	token, _, err := f.authenticate(req.Spec(), req.Header())
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.users, token)
	return connect.NewResponse(&userv1.LogoutResponse{}), nil
}

type fakeOAuthService struct {
	oauthv1connect.UnimplementedOAuthServiceHandler

	api *fakeAPI
}

func (o *fakeOAuthService) RefreshToken(
	_ context.Context,
	req *connect.Request[oauthv1.RefreshTokenRequest],
) (*connect.Response[oauthv1.RefreshTokenResponse], error) {
	f := o.api
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(req.Spec(), ""); err != nil {
		return nil, err
	}
	if req.Msg.GetRefreshToken() != fakeAPIRefreshToken {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("fakeapi: unknown refresh token"))
	}
	f.users[fakeAPIRefreshedToken] = f.users[fakeAPIToken]
	resp := &oauthv1.RefreshTokenResponse{
		LocoToken:    fakeAPIRefreshedToken,
		RefreshToken: fakeAPIRefreshToken,
		ExpiresIn:    3600,
	}
	return connect.NewResponse(resp), nil
}
