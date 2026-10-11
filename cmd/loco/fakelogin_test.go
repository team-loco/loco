package loco

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
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
	mux.Handle(orgv1connect.NewOrgServiceHandler(&fakeOrgService{api: f}))
	mux.Handle(workspacev1connect.NewWorkspaceServiceHandler(&fakeWorkspaceService{api: f}))
}

func (o *fakeAuthService) StartDeviceLogin(
	_ context.Context,
	req *connect.Request[authv1.StartDeviceLoginRequest],
) (*connect.Response[authv1.StartDeviceLoginResponse], error) {
	f := o.api
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(req.Spec(), ""); err != nil {
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
	return connect.NewResponse(resp), nil
}

func (o *fakeAuthService) PollDeviceLogin(
	_ context.Context,
	req *connect.Request[authv1.PollDeviceLoginRequest],
) (*connect.Response[authv1.PollDeviceLoginResponse], error) {
	f := o.api
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(req.Spec(), ""); err != nil {
		return nil, err
	}
	if req.Msg.GetDeviceCode() != fakeDeviceCode || f.deviceLogin != deviceLoginApproved {
		return connect.NewResponse(&authv1.PollDeviceLoginResponse{}), nil
	}
	tokens := &authv1.CLITokens{
		AccessToken:  fakeAPIToken,
		RefreshToken: fakeAPIRefreshToken,
		ExpiresIn:    fakeTokenExpiresIn,
	}
	return connect.NewResponse(&authv1.PollDeviceLoginResponse{Tokens: tokens}), nil
}

type fakeOrgService struct {
	orgv1connect.UnimplementedOrgServiceHandler

	api *fakeAPI
}

func (s *fakeOrgService) ListUserOrgs(
	_ context.Context,
	req *connect.Request[orgv1.ListUserOrgsRequest],
) (*connect.Response[orgv1.ListUserOrgsResponse], error) {
	if _, _, err := s.api.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	org := &orgv1.Organization{Id: fakeOrgID, Name: fakeOrgName}
	return connect.NewResponse(&orgv1.ListUserOrgsResponse{Orgs: []*orgv1.Organization{org}}), nil
}

type fakeWorkspaceService struct {
	workspacev1connect.UnimplementedWorkspaceServiceHandler

	api *fakeAPI
}

func (s *fakeWorkspaceService) ListOrgWorkspaces(
	_ context.Context,
	req *connect.Request[workspacev1.ListOrgWorkspacesRequest],
) (*connect.Response[workspacev1.ListOrgWorkspacesResponse], error) {
	if _, _, err := s.api.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	ws := &workspacev1.Workspace{Id: fakeWorkspaceID, OrgId: fakeOrgID, Name: fakeWorkspaceName}
	resp := &workspacev1.ListOrgWorkspacesResponse{Workspaces: []*workspacev1.Workspace{ws}}
	return connect.NewResponse(resp), nil
}
