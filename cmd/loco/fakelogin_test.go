package main

import (
	"context"
	"net/http"

	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/rogpeppe/go-internal/testscript"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

const (
	fakeDeviceCode      = "fake-device-code"
	fakeUserCode        = "ABCD-EFGH"
	fakeVerificationURI = "https://loco.test/device"
	fakeOrgID           = "1"
	fakeOrgName         = "acme"
	fakeWorkspaceName   = "production"
	fakeIssuer          = "https://idp.loco.test"
	fakeClientID        = "loco-cli"

	deviceLoginApproved = "approved"
	deviceLoginExpired  = "expired"

	fakeApprovedExpiresIn = 60
	fakeExpiredExpiresIn  = 1
	fakeExpiredInterval   = 1
	fakeTokenExpiresIn    = 3600
)

func cmdFakeLogin(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) bool {
	switch args[0] {
	case "identity-provider":
		requireArgs(ts, neg, args, 1, "fakeapi identity-provider")
		api.mu.Lock()
		api.identityProvider = true
		api.mu.Unlock()
	case "device-login":
		requireArgs(ts, neg, args, 2, "fakeapi device-login <approved|expired>")
		api.mu.Lock()
		api.deviceLogin = args[1]
		api.mu.Unlock()
	default:
		return false
	}
	return true
}

func (f *fakeAPI) registerLogin(mux *http.ServeMux) {
	server := connect.NewServer()
	orgv1connect.RegisterOrgServiceHandler(server, &fakeOrgService{api: f})
	workspacev1connect.RegisterWorkspaceServiceHandler(server, &fakeWorkspaceService{api: f})
	connecthttp.Mount(mux, server)
}

func (o *fakeAuthService) StartDeviceLogin(
	_ context.Context,
	_ *authv1.StartDeviceLoginRequest,
) (*authv1.StartDeviceLoginResponse, error) {
	f := o.api
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(connect.Spec{Procedure: authv1connect.AuthServiceStartDeviceLoginProcedure}, ""); err != nil {
		return nil, err
	}
	resp := &authv1.StartDeviceLoginResponse{
		DeviceCode:      fakeDeviceCode,
		UserCode:        fakeUserCode,
		VerificationUri: fakeVerificationURI,
		ExpiresIn:       fakeApprovedExpiresIn,
	}
	if f.deviceLogin == deviceLoginExpired {
		resp.ExpiresIn = fakeExpiredExpiresIn
		resp.Interval = fakeExpiredInterval
	}
	return resp, nil
}

func (o *fakeAuthService) PollDeviceLogin(
	_ context.Context,
	req *authv1.PollDeviceLoginRequest,
) (*authv1.PollDeviceLoginResponse, error) {
	f := o.api
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(connect.Spec{Procedure: authv1connect.AuthServicePollDeviceLoginProcedure}, ""); err != nil {
		return nil, err
	}
	if req.GetDeviceCode() != fakeDeviceCode || f.deviceLogin != deviceLoginApproved {
		return &authv1.PollDeviceLoginResponse{}, nil
	}
	tokens := &authv1.CLITokens{
		AccessToken:  fakeAPIToken,
		RefreshToken: fakeAPIRefreshToken,
		ExpiresIn:    fakeTokenExpiresIn,
	}
	return &authv1.PollDeviceLoginResponse{Tokens: tokens}, nil
}

type fakeOrgService struct {
	orgv1connect.UnimplementedOrgServiceHandler

	api *fakeAPI
}

func (s *fakeOrgService) ListUserOrgs(
	ctx context.Context,
	_ *orgv1.ListUserOrgsRequest,
) (*orgv1.ListUserOrgsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if _, _, err := s.api.authenticate(
		connect.Spec{Procedure: orgv1connect.OrgServiceListUserOrgsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	org := &orgv1.Organization{Id: fakeOrgID, Name: fakeOrgName}
	return &orgv1.ListUserOrgsResponse{Orgs: []*orgv1.Organization{org}}, nil
}

type fakeWorkspaceService struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler

	api *fakeAPI
}

func (s *fakeWorkspaceService) ListOrgWorkspaces(
	ctx context.Context,
	_ *workspacev1.ListOrgWorkspacesRequest,
) (*workspacev1.ListOrgWorkspacesResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if _, _, err := s.api.authenticate(
		connect.Spec{Procedure: workspacev1connect.WorkspaceServiceListOrgWorkspacesProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	ws := &workspacev1.Workspace{Id: fakeWorkspaceID, OrgId: fakeOrgID, Name: fakeWorkspaceName}
	resp := &workspacev1.ListOrgWorkspacesResponse{Workspaces: []*workspacev1.Workspace{ws}}
	return resp, nil
}
