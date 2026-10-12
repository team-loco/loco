package cmdutil

import (
	"context"

	"github.com/team-loco/loco/internal/rpc"

	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func RevokeToken(ctx context.Context, host, token string) error {
	userClients := rpc.ForHost(host, token)
	userClient := userClients.User
	req := &userv1.LogoutRequest{}

	_, err := userClient.Logout(ctx, req)
	return err
}
