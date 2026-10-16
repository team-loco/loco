package token

import (
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
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
)

type createDeps struct {
	NewTokenClient func(host, token string) tokenv1connect.TokenServiceClient
	NewUserClient  func(host, token string) userv1connect.UserServiceClient
	Output         io.Writer
}

func buildCreateCmd() *cobra.Command {
	deps := createDeps{
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
	return newCreateCmd(deps)
}

func newCreateCmd(deps createDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new API token",
		Long: `Create a new access token with specified scopes.

The token string is only displayed once upon creation - save it securely.

Tokens can be scoped to different entity types:
  - user (default): Personal tokens for your account
  - org: Organization-level tokens
  - workspace: Workspace-scoped tokens
  - resource: Resource-specific tokens`,
		Args: cobra.ExactArgs(1),
		Example: `  # Create a personal API token with read access
  loco token create my-ci-token --scope read

  # Create a personal token with write access, expires in 7 days
  loco token create deploy-token --scope write --expires 7d

  # Create an organization-scoped token
  loco token create org-deploy-token --entity-type org --entity-id <organization-id> --scope write

  # Create a workspace-scoped token
  loco token create ws-token --entity-type workspace --entity-id <workspace-id> --scope admin`,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			name := args[0]

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

			scopeStrs, err := cmd.Flags().GetStringSlice("scope")
			if err != nil {
				return fmt.Errorf("failed to get scope flag: %w", err)
			}
			var scopes []*tokenv1.EntityScope
			for _, s := range scopeStrs {
				parsedScope, parseErr := parseScope(s)
				if parseErr != nil {
					return parseErr
				}
				scopes = append(scopes, &tokenv1.EntityScope{
					Scope:      parsedScope,
					EntityType: entityType,
					EntityId:   entityID,
				})
			}

			expiresStr, err := cmd.Flags().GetString("expires")
			if err != nil {
				return fmt.Errorf("failed to get expires flag: %w", err)
			}
			expiresSec, err := parseDuration(expiresStr)
			if err != nil {
				return err
			}

			req := &tokenv1.CreateTokenRequest{
				Name:         name,
				EntityType:   entityType,
				EntityId:     entityID,
				Scopes:       scopes,
				ExpiresInSec: expiresSec,
			}

			resp, err := tokenClient.CreateToken(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to create token: %w", err)
			}

			fmt.Fprintln(deps.Output, "Token created successfully!")
			fmt.Fprintln(deps.Output, "")
			fmt.Fprintf(deps.Output, "Name: %s\n", resp.GetTokenMetadata().GetName())
			fmt.Fprintf(deps.Output, "Entity: %s (ID: %s)\n", entityTypeStr, entityID)
			fmt.Fprintf(deps.Output, "Token: %s\n", resp.GetToken())
			fmt.Fprintln(deps.Output, "")
			fmt.Fprintln(deps.Output, "Save this token - it won't be shown again!")
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().StringSlice("scope", []string{"read"}, "Token scopes: read, write, admin")
	cmd.Flags().String("expires", "30d", "Token expiration (e.g., 1d, 7d, 30d)")
	cmd.Flags().String("entity-type", "user", "Entity type: user, org, workspace, resource")
	cmd.Flags().String("entity-id", "", "Entity ID (required for non-user entity types)")

	return cmd
}

func parseEntityType(s string) (tokenv1.EntityType, error) {
	switch strings.ToLower(s) {
	case "user":
		return tokenv1.EntityType_ENTITY_TYPE_USER, nil
	case "org":
		return tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, nil
	case "organization":
		return tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, nil
	case "workspace":
		return tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, nil
	case "ws":
		return tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, nil
	case "resource":
		return tokenv1.EntityType_ENTITY_TYPE_RESOURCE, nil
	default:
		return tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED, fmt.Errorf(
			"invalid entity type %q: must be user, org, workspace, or resource",
			s,
		)
	}
}

func parseScope(s string) (tokenv1.Scope, error) {
	switch strings.ToLower(s) {
	case "read":
		return tokenv1.Scope_SCOPE_READ, nil
	case "write":
		return tokenv1.Scope_SCOPE_WRITE, nil
	case "admin":
		return tokenv1.Scope_SCOPE_ADMIN, nil
	default:
		return tokenv1.Scope_SCOPE_UNSPECIFIED, fmt.Errorf("invalid scope %q: must be read, write, or admin", s)
	}
}

func parseDuration(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if before, ok := strings.CutSuffix(s, "d"); ok {
		days := before
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return int64(d) * 24 * 60 * 60, nil
	}

	dur, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: use format like 1d, 7d, 30d or Go duration", s)
	}
	return int64(dur.Seconds()), nil
}
