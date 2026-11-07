package token

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
)

// BuildTokenCmd creates the "token" parent command with all subcommands.
func BuildTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens",
		Long: `Commands for creating, listing, and managing access tokens.

Tokens can be scoped to different entity types:
  - user: Personal tokens for your account
  - org: Organization-level tokens
  - workspace: Workspace-scoped tokens
  - resource: Resource-specific tokens`,
		Example: `  # Show your current session token
  loco token show

  # Create a personal API token
  loco token create my-ci-token --scope read

  # Create an organization-scoped token
  loco token create deploy-token --entity-type org --entity-id <organization-id> --scope write

  # List all your personal tokens
  loco token list

  # List tokens for an organization
  loco token list --entity-type org --entity-id <organization-id>

  # Delete a token
  loco token delete my-ci-token`,
	}

	cmd.AddCommand(
		buildShowCmd(),
		buildRevokeCmd(),
		buildCreateCmd(),
		buildListCmd(),
		buildDeleteCmd(),
	)

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
