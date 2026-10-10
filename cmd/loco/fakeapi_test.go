package loco

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"

	"connectrpc.com/connect"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
)

var (
	errFakeUnknownToken        = errors.New("fakeapi: unknown token")
	errFakeUnknownRefreshToken = errors.New("fakeapi: unknown refresh token")
)

const (
	fakeAPIToken          = "test-token"
	fakeAPIRefreshToken   = "test-refresh-token"
	fakeAPIRefreshedToken = "refreshed-token"

	fakeDefaultPort        = 8000
	fakeDefaultCPU         = "100m"
	fakeDefaultMemory      = "256Mi"
	fakeDefaultMinReplicas = 1
	fakeDefaultMaxReplicas = 2
	fakeSchemaURL          = "https://loco.test/schemas/loco.v1.json"
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
	platform *fakePlatform
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		users: map[string]*userv1.User{
			fakeAPIToken: {
				Id:    "1",
				Email: "test@loco.build",
				Name:  "Test User",
			},
		},
		failures: map[string]connect.Code{},
		platform: newFakePlatform(),
	}
}

func (f *fakeAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(userv1connect.NewUserServiceHandler(f))
	mux.Handle(authv1connect.NewAuthServiceHandler(&fakeAuthService{api: f}))
	mux.Handle(configv1connect.NewConfigServiceHandler(&fakeConfigService{api: f}))
	f.registerPlatform(mux)
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
		return "", nil, connect.NewError(connect.CodeUnauthenticated, errFakeUnknownToken)
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

type fakeAuthService struct {
	authv1connect.UnimplementedAuthServiceHandler

	api *fakeAPI
}

func (o *fakeAuthService) RefreshCLIToken(
	_ context.Context,
	req *connect.Request[authv1.RefreshCLITokenRequest],
) (*connect.Response[authv1.RefreshCLITokenResponse], error) {
	f := o.api
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(req.Spec(), ""); err != nil {
		return nil, err
	}
	if req.Msg.GetRefreshToken() != fakeAPIRefreshToken {
		return nil, connect.NewError(connect.CodeUnauthenticated, errFakeUnknownRefreshToken)
	}
	f.users[fakeAPIRefreshedToken] = f.users[fakeAPIToken]
	resp := &authv1.RefreshCLITokenResponse{Tokens: &authv1.CLITokens{
		AccessToken:  fakeAPIRefreshedToken,
		RefreshToken: fakeAPIRefreshToken,
		ExpiresIn:    3600,
	}}
	return connect.NewResponse(resp), nil
}

type fakeConfigService struct {
	configv1connect.UnimplementedConfigServiceHandler

	api *fakeAPI
}

func (c *fakeConfigService) GetConfig(
	_ context.Context,
	req *connect.Request[configv1.GetConfigRequest],
) (*connect.Response[configv1.GetConfigResponse], error) {
	if err := c.api.record(req.Spec(), ""); err != nil {
		return nil, err
	}
	platformDomain := fakePlatformDomain
	c.api.mu.Lock()
	if c.api.platform.noPlatformDomain {
		platformDomain = ""
	}
	c.api.mu.Unlock()
	defaults := &configv1.DefaultServiceConfig{
		Routing:        &resourcev1.RoutingConfig{Port: fakeDefaultPort},
		Cpu:            fakeDefaultCPU,
		Memory:         fakeDefaultMemory,
		MinReplicas:    fakeDefaultMinReplicas,
		MaxReplicas:    fakeDefaultMaxReplicas,
		PlatformDomain: platformDomain,
	}
	resp := &configv1.GetConfigResponse{ServiceDefaults: defaults, SchemaUrl: fakeSchemaURL}
	return connect.NewResponse(resp), nil
}
