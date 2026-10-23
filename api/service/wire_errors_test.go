package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
	"github.com/team-loco/loco/gen/go/loco/webhook/v1/webhookv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

const expectedPermissionMessage = "permission denied"
const wireEntityID = "11111111-2222-7333-8444-555555555555"

type wireOwnership struct{ genDb.Querier }

func (*wireOwnership) GetOrganizationIDByWorkspaceID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.MustParse(wireEntityID), nil
}

func (*wireOwnership) GetWorkspaceOrganizationIDByResourceID(
	context.Context,
	uuid.UUID,
) (genDb.GetWorkspaceOrganizationIDByResourceIDRow, error) {
	id := uuid.MustParse(wireEntityID)
	return genDb.GetWorkspaceOrganizationIDByResourceIDRow{WorkspaceID: id, OrgID: id}, nil
}
func TestServicesExposePublicWireErrors(t *testing.T) {
	authorizer := authz.New(nil, &wireOwnership{})
	ctx := context.WithValue(
		t.Context(),
		contextkeys.EntityKey,
		genDb.Entity{Type: genDb.EntityTypeUser, ID: uuid.MustParse(wireEntityID)},
	)
	ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, []genDb.EntityScope{})
	cases := []struct {
		name     string
		register func(*connect.Server)
		call     func(context.Context, *connect.Client) error
		code     connect.Code
		message  string
	}{
		{name: "User", register: func(server *connect.Server) {
			userv1connect.RegisterUserServiceHandler(server, &UserServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := userv1connect.NewUserServiceClient(client)
				request := &userv1.ListUsersRequest{}
				_, err := serviceClient.ListUsers(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Org", register: func(server *connect.Server) {
			orgv1connect.RegisterOrgServiceHandler(server, &OrgServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := orgv1connect.NewOrgServiceClient(client)
				request := &orgv1.CreateOrgRequest{}
				_, err := serviceClient.CreateOrg(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Workspace", register: func(server *connect.Server) {
			workspacev1connect.RegisterWorkspaceServiceHandler(server, &WorkspaceServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := workspacev1connect.NewWorkspaceServiceClient(client)
				request := &workspacev1.GetWorkspaceRequest{WorkspaceId: wireEntityID}
				_, err := serviceClient.GetWorkspace(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Resource", register: func(server *connect.Server) {
			resourcev1connect.RegisterResourceServiceHandler(server, &ResourceServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := resourcev1connect.NewResourceServiceClient(client)
				request := &resourcev1.GetResourceRequest{
					Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: wireEntityID},
				}
				_, err := serviceClient.GetResource(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Deployment", register: func(server *connect.Server) {
			deploymentv1connect.RegisterDeploymentServiceHandler(server, &DeploymentServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := deploymentv1connect.NewDeploymentServiceClient(client)
				request := &deploymentv1.ListDeploymentsRequest{ResourceId: wireEntityID}
				_, err := serviceClient.ListDeployments(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Domain", register: func(server *connect.Server) {
			domainv1connect.RegisterDomainServiceHandler(server, &DomainServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := domainv1connect.NewDomainServiceClient(client)
				request := &domainv1.ListLocoOwnedDomainsRequest{}
				_, err := serviceClient.ListLocoOwnedDomains(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Token", register: func(server *connect.Server) {
			tokenv1connect.RegisterTokenServiceHandler(server, &TokenServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := tokenv1connect.NewTokenServiceClient(client)
				request := &tokenv1.ListTokensRequest{
					EntityType: tokenv1.EntityType_ENTITY_TYPE_USER,
					EntityId:   wireEntityID,
				}
				_, err := serviceClient.ListTokens(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Environment", register: func(server *connect.Server) {
			environmentv1connect.RegisterEnvironmentServiceHandler(server, &EnvironmentServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := environmentv1connect.NewEnvironmentServiceClient(client)
				request := &environmentv1.ListEnvironmentsRequest{WorkspaceId: wireEntityID}
				_, err := serviceClient.ListEnvironments(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Event", register: func(server *connect.Server) {
			eventv1connect.RegisterEventServiceHandler(server, &EventServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := eventv1connect.NewEventServiceClient(client)
				request := &eventv1.ListEventsRequest{OrgId: wireEntityID}
				_, err := serviceClient.ListEvents(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Webhook", register: func(server *connect.Server) {
			webhookv1connect.RegisterWebhookServiceHandler(server, &WebhookServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := webhookv1connect.NewWebhookServiceClient(client)
				request := &webhookv1.ListWebhooksRequest{WorkspaceId: wireEntityID}
				_, err := serviceClient.ListWebhooks(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Secret", register: func(server *connect.Server) {
			secretv1connect.RegisterSecretServiceHandler(server, &SecretServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := secretv1connect.NewSecretServiceClient(client)
				request := &secretv1.ListSecretsRequest{ResourceId: wireEntityID, EnvironmentId: wireEntityID}
				_, err := serviceClient.ListSecrets(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{name: "Build", register: func(server *connect.Server) {
			buildv1connect.RegisterBuildServiceHandler(server, &BuildServer{authz: authorizer})
		},
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := buildv1connect.NewBuildServiceClient(client)
				request := &buildv1.ListBuildsRequest{ResourceId: wireEntityID}
				_, err := serviceClient.ListBuilds(ctx, request)
				return err
			}, code: connect.CodePermissionDenied, message: expectedPermissionMessage},
		{
			name:     "Plan",
			register: func(server *connect.Server) { planv1connect.RegisterPlanServiceHandler(server, &PlanServer{}) },
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := planv1connect.NewPlanServiceClient(client)
				request := &planv1.PlanRequest{File: []byte("not: [valid")}
				_, err := serviceClient.Plan(ctx, request)
				return err
			},
			code:    connect.CodeInvalidArgument,
			message: "invalid loco.yaml",
		},
		{
			name:     "Agent",
			register: func(server *connect.Server) { agentv1connect.RegisterAgentServiceHandler(server, &AgentServer{}) },
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := agentv1connect.NewAgentServiceClient(client)
				request := &agentv1.RegisterRequest{}
				_, err := serviceClient.Register(ctx, request)
				return err
			},
			code:    connect.CodeUnauthenticated,
			message: "unauthorized",
		},
		{
			name:     "Auth",
			register: func(server *connect.Server) { authv1connect.RegisterAuthServiceHandler(server, &AuthServer{}) },
			call: func(ctx context.Context, client *connect.Client) error {
				serviceClient := authv1connect.NewAuthServiceClient(client)
				request := &authv1.ApproveCLILoginRequest{}
				_, err := serviceClient.ApproveCLILogin(ctx, request)
				return err
			},
			code:    connect.CodePermissionDenied,
			message: ErrApproveFromWeb.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := connect.NewServer()
			tc.register(server)
			transport := connectinprocess.New(server)
			client := connect.NewClient(transport)
			err := tc.call(ctx, client)
			failure, ok := errors.AsType[*connect.Error](err)
			if !ok {
				t.Fatalf("error = %v", err)
			}
			if failure.Code() != tc.code || failure.Message() != tc.message {
				t.Fatalf("wire error = %v, want %s: %s", failure, tc.code, tc.message)
			}
		})
	}
}
