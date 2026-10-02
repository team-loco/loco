package cmdutil

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/httputil"
)

func RevokeToken(ctx context.Context, host, token string) error {
	httpClient := httputil.NewHTTPClient()
	userClient := userv1connect.NewUserServiceClient(httpClient, host)
	req := connect.NewRequest(&userv1.LogoutRequest{})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", token))
	_, err := userClient.Logout(ctx, req)
	return err
}
