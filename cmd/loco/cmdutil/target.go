package cmdutil

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/session"
)

// Target is the API host a command talks to, the logged-in token and the clients
// built from them, plus the workspace and environment once resolved.
type Target struct {
	Host        string
	Token       string
	Clients     *rpc.Clients
	WorkspaceID string
	Environment *environmentv1.Environment
}

// ResolveLogin resolves the host and the logged-in token, without a workspace.
func ResolveLogin(cmd *cobra.Command) (Target, error) {
	host, err := GetHost(cmd)
	if err != nil {
		return Target{}, err
	}
	locoToken, err := GetCurrentLocoToken(cmd)
	if err != nil {
		return Target{}, err
	}
	clients := rpc.ForHost(host, locoToken.Token)
	return Target{Host: host, Token: locoToken.Token, Clients: clients}, nil
}

// ResolveTarget resolves the host, the logged-in token and the workspace named by
// --org and --workspace or their defaults.
func ResolveTarget(ctx context.Context, cmd *cobra.Command) (Target, error) {
	t, err := ResolveLogin(cmd)
	if err != nil {
		return Target{}, err
	}
	t.WorkspaceID, err = ResolveWorkspaceID(ctx, cmd, session.Load, t.Clients.Workspace)
	if err != nil {
		return Target{}, err
	}
	return t, nil
}

// WithEnvironment resolves the workspace environment named by --env or LOCO_ENV,
// asking in a terminal when interactive is set and several exist.
func (t Target) WithEnvironment(ctx context.Context, cmd *cobra.Command, interactive bool) (Target, error) {
	env, err := ResolveEnvironment(ctx, cmd, t.Clients.Environment, interactive, t.WorkspaceID)
	if err != nil {
		return Target{}, err
	}
	t.Environment = env
	return t, nil
}

// Service returns the workspace's service called name.
func (t Target) Service(ctx context.Context, name string) (*resourcev1.Resource, error) {
	nameKey := &resourcev1.GetResourceNameKey{WorkspaceId: t.WorkspaceID, Name: name}
	req := &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{NameKey: nameKey},
	}
	resp, err := t.Clients.Resource.GetResource(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("service %q not found: %w", name, err)
	}
	return resp.GetResource(), nil
}
