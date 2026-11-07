package token

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func buildListCmd() *cobra.Command {
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

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}

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

			entityID, err := resolveEntityID(ctx, target.Clients.User, entityType, entityTypeStr, requestedEntityID)
			if err != nil {
				return err
			}

			req := &tokenv1.ListTokensRequest{
				EntityType: entityType,
				EntityId:   entityID,
			}

			resp, err := target.Clients.Token.ListTokens(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to list tokens: %w", err)
			}

			tokens := resp.GetTokens()
			if len(tokens) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tokens found.")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Tokens for %s (ID: %s):\n", entityTypeStr, entityID)

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
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s (scopes: %s, expires: %s)\n", t.GetName(), scopes, expiresAt)
			}

			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String("entity-type", "user", "Entity type: user, org, workspace, resource")
	cmd.Flags().String("entity-id", "", "Entity ID (defaults to current user for user type)")

	return cmd
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
