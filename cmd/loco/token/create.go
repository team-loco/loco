package token

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/httputil"
	tokenv1 "github.com/team-loco/loco/proto/loco/token/v1"
	"github.com/team-loco/loco/proto/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/proto/loco/user/v1/userv1connect"
)

type createDeps struct {
	NewTokenClient func(host string) tokenv1connect.TokenServiceClient
	NewUserClient  func(host string) userv1connect.UserServiceClient
	Output         io.Writer
}

func buildCreateCmd() *cobra.Command {
	deps := createDeps{
		NewTokenClient: func(host string) tokenv1connect.TokenServiceClient {
			return tokenv1connect.NewTokenServiceClient(httputil.NewHTTPClient(), host)
		},
		NewUserClient: func(host string) userv1connect.UserServiceClient {
			return userv1connect.NewUserServiceClient(httputil.NewHTTPClient(), host)
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
  loco token create org-deploy-token --entity-type org --entity-id 123 --scope write

  # Create a workspace-scoped token
  loco token create ws-token --entity-type workspace --entity-id 456 --scope admin`,
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

			name := args[0]

			entityTypeStr, err := cmd.Flags().GetString("entity-type")
			if err != nil {
				return fmt.Errorf("failed to get entity-type flag: %w", err)
			}
			entityIDInt, err := cmd.Flags().GetInt64("entity-id")
			if err != nil {
				return fmt.Errorf("failed to get entity-id flag: %w", err)
			}

			entityType, err := parseEntityType(entityTypeStr)
			if err != nil {
				return err
			}

			var entityID string
			if entityType == tokenv1.EntityType_ENTITY_TYPE_USER && entityIDInt == 0 {
				entityID, err = getCurrentUserID(ctx, userClient, authHeader)
				if err != nil {
					return err
				}
			} else if entityIDInt == 0 {
				return fmt.Errorf("--entity-id is required for entity type %q", entityTypeStr)
			} else {
				entityID = fmt.Sprintf("%d", entityIDInt)
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

			req := connect.NewRequest(&tokenv1.CreateTokenRequest{
				Name:         name,
				EntityType:   entityType,
				EntityId:     entityID,
				Scopes:       scopes,
				ExpiresInSec: expiresSec,
			})
			req.Header().Set("Authorization", authHeader)

			resp, err := tokenClient.CreateToken(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to create token: %w", err)
			}

			fmt.Fprintln(deps.Output, "Token created successfully!")
			fmt.Fprintln(deps.Output, "")
			fmt.Fprintf(deps.Output, "Name: %s\n", resp.Msg.TokenMetadata.Name)
			fmt.Fprintf(deps.Output, "Entity: %s (ID: %s)\n", entityTypeStr, entityID)
			fmt.Fprintf(deps.Output, "Token: %s\n", resp.Msg.Token)
			fmt.Fprintln(deps.Output, "")
			fmt.Fprintln(deps.Output, "Save this token - it won't be shown again!")
			return nil
		},
	}

	cmd.Flags().StringSlice("scope", []string{"read"}, "Token scopes: read, write, admin")
	cmd.Flags().String("expires", "30d", "Token expiration (e.g., 1d, 7d, 30d)")
	cmd.Flags().String("entity-type", "user", "Entity type: user, org, workspace, resource")
	cmd.Flags().Int64("entity-id", 0, "Entity ID (required for non-user entity types)")

	return cmd
}

func parseEntityType(s string) (tokenv1.EntityType, error) {
	switch strings.ToLower(s) {
	case "user":
		return tokenv1.EntityType_ENTITY_TYPE_USER, nil
	case "org", "organization":
		return tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, nil
	case "workspace", "ws":
		return tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, nil
	case "resource":
		return tokenv1.EntityType_ENTITY_TYPE_RESOURCE, nil
	default:
		return tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED, fmt.Errorf("invalid entity type %q: must be user, org, workspace, or resource", s)
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
