package main

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/rpcerr"
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

	identityProvider bool
	deviceLogin      string
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
	server := connect.NewServer()
	userv1connect.RegisterUserServiceHandler(server, f)
	authv1connect.RegisterAuthServiceHandler(server, &fakeAuthService{api: f})
	configv1connect.RegisterConfigServiceHandler(server, &fakeConfigService{api: f})
	connecthttp.Mount(mux, server)

	f.registerPlatform(mux)
	f.registerLogin(mux)
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
		return connect.Errorf(code, "fakeapi: %s configured to fail", method)
	}
	return nil
}

func (f *fakeAPI) authenticate(
	spec connect.Spec,
	header httputil.HeaderGetter,
) (string, *userv1.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	authorization := header.Get("Authorization")
	if err := f.record(spec, authorization); err != nil {
		return "", nil, err
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	usr, ok := f.users[token]
	if !ok {
		return "", nil, rpcerr.New(connect.CodeUnauthenticated, errFakeUnknownToken)
	}
	return token, usr, nil
}

func (f *fakeAPI) WhoAmI(
	ctx context.Context,
	_ *userv1.WhoAmIRequest,
) (*userv1.WhoAmIResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	_, usr, err := f.authenticate(
		connect.Spec{Procedure: userv1connect.UserServiceWhoAmIProcedure},
		info.RequestHeader(),
	)
	if err != nil {
		return nil, err
	}
	return &userv1.WhoAmIResponse{User: usr}, nil
}

func (f *fakeAPI) Logout(
	ctx context.Context,
	_ *userv1.LogoutRequest,
) (*userv1.LogoutResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	token, _, err := f.authenticate(
		connect.Spec{Procedure: userv1connect.UserServiceLogoutProcedure},
		info.RequestHeader(),
	)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.users, token)
	return &userv1.LogoutResponse{}, nil
}

type fakeAuthService struct {
	authv1connect.UnimplementedAuthServiceHandler

	api *fakeAPI
}

func (o *fakeAuthService) RefreshCLIToken(
	_ context.Context,
	req *authv1.RefreshCLITokenRequest,
) (*authv1.RefreshCLITokenResponse, error) {
	f := o.api
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(connect.Spec{Procedure: authv1connect.AuthServiceRefreshCLITokenProcedure}, ""); err != nil {
		return nil, err
	}
	if req.GetRefreshToken() != fakeAPIRefreshToken {
		return nil, rpcerr.New(connect.CodeUnauthenticated, errFakeUnknownRefreshToken)
	}
	f.users[fakeAPIRefreshedToken] = f.users[fakeAPIToken]
	resp := &authv1.RefreshCLITokenResponse{Tokens: &authv1.CLITokens{
		AccessToken:  fakeAPIRefreshedToken,
		RefreshToken: fakeAPIRefreshToken,
		ExpiresIn:    3600,
	}}
	return resp, nil
}

type fakeConfigService struct {
	configv1connect.UnimplementedConfigServiceHandler

	api *fakeAPI
}

func (c *fakeConfigService) GetConfig(
	_ context.Context,
	_ *configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	if err := c.api.record(connect.Spec{Procedure: configv1connect.ConfigServiceGetConfigProcedure}, ""); err != nil {
		return nil, err
	}
	platformDomain := fakePlatformDomain
	var auth *configv1.AuthConfig
	c.api.mu.Lock()
	if c.api.platform.noPlatformDomain {
		platformDomain = ""
	}
	if c.api.identityProvider {
		auth = &configv1.AuthConfig{Issuer: fakeIssuer, ClientId: fakeClientID}
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
	resp := &configv1.GetConfigResponse{ServiceDefaults: defaults, SchemaUrl: fakeSchemaURL, Auth: auth}
	return resp, nil
}
