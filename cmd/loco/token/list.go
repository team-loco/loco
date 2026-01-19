package token

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/shared"
	tokenv1 "github.com/team-loco/loco/shared/proto/loco/token/v1"
	"github.com/team-loco/loco/shared/proto/loco/token/v1/tokenv1connect"
	userv1 "github.com/team-loco/loco/shared/proto/loco/user/v1"
	"github.com/team-loco/loco/shared/proto/loco/user/v1/userv1connect"
)

type listDeps struct {
	NewTokenClient func(host string) tokenv1connect.TokenServiceClient
	NewUserClient  func(host string) userv1connect.UserServiceClient
	Output         io.Writer
}

func buildListCmd() *cobra.Command {
	deps := listDeps{
		NewTokenClient: func(host string) tokenv1connect.TokenServiceClient {
			return tokenv1connect.NewTokenServiceClient(shared.NewHTTPClient(), host)
		},
		NewUserClient: func(host string) userv1connect.UserServiceClient {
			return userv1connect.NewUserServiceClient(shared.NewHTTPClient(), host)
		},
		Output: os.Stdout,
	}
	return newListCmd(deps)
}

func newListCmd(deps listDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API tokens",
		Long:  "List all access tokens for an entity.",
		Args:  cobra.NoArgs,
		Example: `  # List all your personal tokens
  loco token list

  # List tokens for an organization
  loco token list --entity-type org --entity-id 123

  # List tokens for a workspace
  loco token list --entity-type workspace --entity-id 456`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken()
			if err != nil {
				return err
			}

			tokenClient := deps.NewTokenClient(host)
			userClient := deps.NewUserClient(host)
			authHeader := fmt.Sprintf("Bearer %s", locoToken.Token)

			entityTypeStr, _ := cmd.Flags().GetString("entity-type")
			entityID, _ := cmd.Flags().GetInt64("entity-id")

			entityType, err := parseEntityType(entityTypeStr)
			if err != nil {
				return err
			}

			if entityType == tokenv1.EntityType_ENTITY_TYPE_USER && entityID == 0 {
				entityID, err = getCurrentUserID(ctx, userClient, authHeader)
				if err != nil {
					return err
				}
			} else if entityID == 0 {
				return fmt.Errorf("--entity-id is required for entity type %q", entityTypeStr)
			}

			req := connect.NewRequest(&tokenv1.ListTokensRequest{
				EntityType: entityType,
				EntityId:   entityID,
			})
			req.Header().Set("Authorization", authHeader)

			resp, err := tokenClient.ListTokens(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to list tokens: %w", err)
			}

			tokens := resp.Msg.Tokens
			if len(tokens) == 0 {
				fmt.Fprintln(deps.Output, "No tokens found.")
				return nil
			}

			fmt.Fprintf(deps.Output, "Tokens for %s (ID: %d):\n", entityTypeStr, entityID)

			for _, t := range tokens {
				expiresAt := "never"
				if t.ExpiresAt != nil {
					exp := t.ExpiresAt.AsTime()
					if exp.Before(time.Now()) {
						expiresAt = fmt.Sprintf("%s (expired)", exp.Format("2006-01-02"))
					} else {
						expiresAt = exp.Format("2006-01-02")
					}
				}

				scopes := formatScopes(t.Scopes)
				fmt.Fprintf(deps.Output, "  - %s (scopes: %s, expires: %s)\n", t.Name, scopes, expiresAt)
			}

			return nil
		},
	}

	cmd.Flags().String("entity-type", "user", "Entity type: user, org, workspace, resource")
	cmd.Flags().Int64("entity-id", 0, "Entity ID (defaults to current user for user type)")

	return cmd
}

func getCurrentUserID(ctx context.Context, userClient userv1connect.UserServiceClient, authHeader string) (int64, error) {
	whoAmIReq := connect.NewRequest(&userv1.WhoAmIRequest{})
	whoAmIReq.Header().Set("Authorization", authHeader)

	whoAmIResp, err := userClient.WhoAmI(ctx, whoAmIReq)
	if err != nil {
		return 0, fmt.Errorf("failed to get current user: %w", err)
	}
	return whoAmIResp.Msg.User.Id, nil
}

func formatScopes(scopes []*tokenv1.EntityScope) string {
	if len(scopes) == 0 {
		return "none"
	}

	var scopeNames []string
	for _, s := range scopes {
		switch s.Scope {
		case tokenv1.Scope_SCOPE_READ:
			scopeNames = append(scopeNames, "read")
		case tokenv1.Scope_SCOPE_WRITE:
			scopeNames = append(scopeNames, "write")
		case tokenv1.Scope_SCOPE_ADMIN:
			scopeNames = append(scopeNames, "admin")
		}
	}

	if len(scopeNames) == 0 {
		return "none"
	}

	result := scopeNames[0]
	for i := 1; i < len(scopeNames); i++ {
		result += ", " + scopeNames[i]
	}
	return result
}
