package rpc

import (
	"context"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/gen/go/loco/webhook/v1/webhookv1connect"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/httputil"
)

type Clients struct {
	ClusterAdmin       clusterv1connect.ClusterAdminServiceClient
	Agent              agentv1connect.AgentServiceClient
	Auth               authv1connect.AuthServiceClient
	Build              buildv1connect.BuildServiceClient
	Config             configv1connect.ConfigServiceClient
	Deployment         deploymentv1connect.DeploymentServiceClient
	Domain             domainv1connect.DomainServiceClient
	Environment        environmentv1connect.EnvironmentServiceClient
	Event              eventv1connect.EventServiceClient
	Org                orgv1connect.OrgServiceClient
	Plan               planv1connect.PlanServiceClient
	Resource           resourcev1connect.ResourceServiceClient
	Secret             secretv1connect.SecretServiceClient
	Token              tokenv1connect.TokenServiceClient
	User               userv1connect.UserServiceClient
	Webhook            webhookv1connect.WebhookServiceClient
	Workspace          workspacev1connect.WorkspaceServiceClient
	ObservabilityProxy observabilityv1connect.ObservabilityProxyServiceClient
}

var defaultHTTPClient = httputil.NewHTTPClient()

func ForHost(host, token string) *Clients {
	return NewHTTPClients(defaultHTTPClient, host, token)
}

func NewHTTPClients(httpClient *http.Client, host, token string) *Clients {
	transport := connecthttp.NewTransport(httpClient, host)
	bearerInterceptor := BearerInterceptor(token)
	connectClient := connect.NewClient(transport, bearerInterceptor)
	return NewClients(connectClient)
}

func NewClients(connectClient *connect.Client) *Clients {
	return &Clients{
		ClusterAdmin:       clusterv1connect.NewClusterAdminServiceClient(connectClient),
		Agent:              agentv1connect.NewAgentServiceClient(connectClient),
		Auth:               authv1connect.NewAuthServiceClient(connectClient),
		Build:              buildv1connect.NewBuildServiceClient(connectClient),
		Config:             configv1connect.NewConfigServiceClient(connectClient),
		Deployment:         deploymentv1connect.NewDeploymentServiceClient(connectClient),
		Domain:             domainv1connect.NewDomainServiceClient(connectClient),
		Environment:        environmentv1connect.NewEnvironmentServiceClient(connectClient),
		Event:              eventv1connect.NewEventServiceClient(connectClient),
		Org:                orgv1connect.NewOrgServiceClient(connectClient),
		Plan:               planv1connect.NewPlanServiceClient(connectClient),
		Resource:           resourcev1connect.NewResourceServiceClient(connectClient),
		Secret:             secretv1connect.NewSecretServiceClient(connectClient),
		Token:              tokenv1connect.NewTokenServiceClient(connectClient),
		User:               userv1connect.NewUserServiceClient(connectClient),
		Webhook:            webhookv1connect.NewWebhookServiceClient(connectClient),
		Workspace:          workspacev1connect.NewWorkspaceServiceClient(connectClient),
		ObservabilityProxy: observabilityv1connect.NewObservabilityProxyServiceClient(connectClient),
	}
}
func BearerInterceptor(token string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if token != "" {
				info, ok := connect.CallInfoForClientContext(ctx)
				if !ok {
					ctx, info = connect.NewClientContext(ctx)
				}
				info.RequestHeader().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, spec)
		}
	}
}
