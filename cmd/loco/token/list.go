package token

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/team-loco/loco/internal/rpc"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
)

type listDeps struct {
	NewTokenClient func(host, token string) tokenv1connect.TokenServiceClient
	NewUserClient  func(host, token string) userv1connect.UserServiceClient
	Output         io.Writer
}

func buildListCmd() *cobra.Command {
	deps := listDeps{
		NewTokenClient: func(host, token string) tokenv1connect.TokenServiceClient {
			tokenClients := rpc.ForHost(host, token)
			return tokenClients.Token
		},
		NewUserClient: func(host, token string) userv1connect.UserServiceClient {
			userClients := rpc.ForHost(host, token)
			return userClients.User
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
  loco token list --entity-type org --entity-id <organization-id>

  # List tokens for a workspace
  loco token list --entity-type workspace --entity-id <workspace-id>`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
			if err != nil {
				return err
			}

			tokenClient := deps.NewTokenClient(host, locoToken.Token)
			userClient := deps.NewUserClient(host, locoToken.Token)

			entityTypeStr, err := cmd.Flags().GetString("entity-type")
			if err != nil {
				return fmt.Errorf("failed to get entity-type flag: %w", err)
			}
			requestedEntityID, err := cmd.Flags().GetString("entity-id")
			if err != nil {
				return fmt.Errorf("failed to get entity-id flag: %w", err)
			}

			entityType, err := parseEntityType(entityTypeStr)
			if err != nil {
				return err
			}

			entityID, err := resolveEntityID(ctx, userClient, entityType, entityTypeStr, requestedEntityID)
			if err != nil {
				return err
			}

			req := &tokenv1.ListTokensRequest{
				EntityType: entityType,
				EntityId:   entityID,
			}

			resp, err := tokenClient.ListTokens(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to list tokens: %w", err)
			}

			tokens := resp.GetTokens()
			if len(tokens) == 0 {
				fmt.Fprintln(deps.Output, "No tokens found.")
				return nil
			}

			fmt.Fprintf(deps.Output, "Tokens for %s (ID: %s):\n", entityTypeStr, entityID)

			for _, t := range tokens {
				expiresAt := "never"
				if t.GetExpiresAt() != nil {
					exp := t.GetExpiresAt().AsTime()
					if exp.Before(time.Now()) {
						expiresAt = fmt.Sprintf("%s (expired)", exp.Format(time.DateOnly))
					} else {
						expiresAt = exp.Format(time.DateOnly)
					}
				}

				scopes := formatScopes(t.GetScopes())
				fmt.Fprintf(deps.Output, "  - %s (scopes: %s, expires: %s)\n", t.GetName(), scopes, expiresAt)
			}

			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String("entity-type", "user", "Entity type: user, org, workspace, resource")
	cmd.Flags().String("entity-id", "", "Entity ID (defaults to current user for user type)")

	return cmd
}

func resolveEntityID(
	ctx context.Context,
	userClient userv1connect.UserServiceClient,
	entityType tokenv1.EntityType,
	entityTypeStr string,
	entityID string,
) (string, error) {
	switch {
	case entityID != "":
		return entityID, nil
	case entityType == tokenv1.EntityType_ENTITY_TYPE_USER:
		return getCurrentUserID(ctx, userClient)
	default:
		return "", fmt.Errorf("--entity-id is required for entity type %q", entityTypeStr)
	}
}

func getCurrentUserID(
	ctx context.Context,
	userClient userv1connect.UserServiceClient,
) (string, error) {
	whoAmIReq := &userv1.WhoAmIRequest{}

	whoAmIResp, err := userClient.WhoAmI(ctx, whoAmIReq)
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}
	return whoAmIResp.GetUser().GetId(), nil
}

func formatScopes(scopes []*tokenv1.EntityScope) string {
	if len(scopes) == 0 {
		return "none"
	}

	var scopeNames []string
	for _, s := range scopes {
		switch s.GetScope() {
		case tokenv1.Scope_SCOPE_READ:
			scopeNames = append(scopeNames, "read")
		case tokenv1.Scope_SCOPE_WRITE:
			scopeNames = append(scopeNames, "write")
		case tokenv1.Scope_SCOPE_ADMIN:
			scopeNames = append(scopeNames, "admin")
		case tokenv1.Scope_SCOPE_MEMBER:
			scopeNames = append(scopeNames, "member")
		}
	}

	if len(scopeNames) == 0 {
		return "none"
	}

	return strings.Join(scopeNames, ", ")
}
