package cmdutil

import (
	"context"

	"github.com/team-loco/loco/internal/rpc"

	"connectrpc.com/connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func RevokeToken(ctx context.Context, host, token string) error {
	userClients := rpc.ForHost(host, token)
	userClient := userClients.User
	req := connect.NewRequest(&userv1.LogoutRequest{})

	_, err := userClient.Logout(ctx, req)
	return err
}
