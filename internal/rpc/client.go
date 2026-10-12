package rpc

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
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
	Agent               agentv1connect.AgentServiceClient
	Auth                authv1connect.AuthServiceClient
	Build               buildv1connect.BuildServiceClient
	Config              configv1connect.ConfigServiceClient
	Deployment          deploymentv1connect.DeploymentServiceClient
	Domain              domainv1connect.DomainServiceClient
	Environment         environmentv1connect.EnvironmentServiceClient
	Event               eventv1connect.EventServiceClient
	Org                 orgv1connect.OrgServiceClient
	Plan                planv1connect.PlanServiceClient
	Resource            resourcev1connect.ResourceServiceClient
	Secret              secretv1connect.SecretServiceClient
	Token               tokenv1connect.TokenServiceClient
	User                userv1connect.UserServiceClient
	Webhook             webhookv1connect.WebhookServiceClient
	Workspace           workspacev1connect.WorkspaceServiceClient
	ObservabilityAccess observabilityv1connect.ObservabilityAccessServiceClient
	ObservabilityProxy  observabilityv1connect.ObservabilityProxyServiceClient
}

var defaultHTTPClient = httputil.NewHTTPClient()

func ForHost(host, token string) *Clients {
	return NewHTTPClients(defaultHTTPClient, host, token)
}
func NewHTTPClients(httpClient *http.Client, host, token string) *Clients {
	option := connect.WithInterceptors(BearerInterceptor(token))
	return &Clients{Agent: agentv1connect.NewAgentServiceClient(httpClient, host, option),
		Auth:                authv1connect.NewAuthServiceClient(httpClient, host, option),
		Build:               buildv1connect.NewBuildServiceClient(httpClient, host, option),
		Config:              configv1connect.NewConfigServiceClient(httpClient, host, option),
		Deployment:          deploymentv1connect.NewDeploymentServiceClient(httpClient, host, option),
		Domain:              domainv1connect.NewDomainServiceClient(httpClient, host, option),
		Environment:         environmentv1connect.NewEnvironmentServiceClient(httpClient, host, option),
		Event:               eventv1connect.NewEventServiceClient(httpClient, host, option),
		Org:                 orgv1connect.NewOrgServiceClient(httpClient, host, option),
		Plan:                planv1connect.NewPlanServiceClient(httpClient, host, option),
		Resource:            resourcev1connect.NewResourceServiceClient(httpClient, host, option),
		Secret:              secretv1connect.NewSecretServiceClient(httpClient, host, option),
		Token:               tokenv1connect.NewTokenServiceClient(httpClient, host, option),
		User:                userv1connect.NewUserServiceClient(httpClient, host, option),
		Webhook:             webhookv1connect.NewWebhookServiceClient(httpClient, host, option),
		Workspace:           workspacev1connect.NewWorkspaceServiceClient(httpClient, host, option),
		ObservabilityAccess: observabilityv1connect.NewObservabilityAccessServiceClient(httpClient, host, option),
		ObservabilityProxy:  observabilityv1connect.NewObservabilityProxyServiceClient(httpClient, host, option)}
}

type bearerInterceptor struct{ token string }

func BearerInterceptor(token string) connect.Interceptor { return &bearerInterceptor{token: token} }
func (i *bearerInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i.token != "" {
			req.Header().Set("Authorization", "Bearer "+i.token)
		}
		return next(ctx, req)
	}
}
func (i *bearerInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		if i.token != "" {
			conn.RequestHeader().Set("Authorization", "Bearer "+i.token)
		}
		return conn
	}
}
func (*bearerInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
