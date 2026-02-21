package token

import (
	"fmt"
	"io"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	tokenv1 "github.com/team-loco/loco/proto/loco/token/v1"
	"github.com/team-loco/loco/proto/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/proto/loco/user/v1/userv1connect"
)

type deleteDeps struct {
	NewTokenClient func(host string) tokenv1connect.TokenServiceClient
	NewUserClient  func(host string) userv1connect.UserServiceClient
	AskYesNo       func(prompt string) (bool, error)
	Output         io.Writer
}

func buildDeleteCmd() *cobra.Command {
	deps := deleteDeps{
		NewTokenClient: func(host string) tokenv1connect.TokenServiceClient {
			return tokenv1connect.NewTokenServiceClient(shared.NewHTTPClient(), host)
		},
		NewUserClient: func(host string) userv1connect.UserServiceClient {
			return userv1connect.NewUserServiceClient(shared.NewHTTPClient(), host)
		},
		AskYesNo: ui.AskYesNo,
		Output:   os.Stdout,
	}
	return newDeleteCmd(deps)
}

func newDeleteCmd(deps deleteDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an API token",
		Long:  "Delete (revoke) an access token by name.",
		Args:  cobra.ExactArgs(1),
		Example: `  # Delete a personal token (with confirmation)
  loco token delete my-ci-token

  # Delete without confirmation
  loco token delete my-ci-token --yes

  # Delete an organization token
  loco token delete org-token --entity-type org --entity-id 123`,
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
			entityID, err := cmd.Flags().GetInt64("entity-id")
			if err != nil {
				return fmt.Errorf("failed to get entity-id flag: %w", err)
			}

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

			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return fmt.Errorf("failed to get yes flag: %w", err)
			}
			if !yes {
				confirm, confirmErr := deps.AskYesNo(fmt.Sprintf("Are you sure you want to delete token %q? This cannot be undone.", name))
				if confirmErr != nil {
					return fmt.Errorf("failed to prompt for confirmation: %w", confirmErr)
				}
				if !confirm {
					fmt.Fprintln(deps.Output, "Aborted.")
					return nil
				}
			}

			req := connect.NewRequest(&tokenv1.RevokeTokenRequest{
				Name:       name,
				EntityType: entityType,
				EntityId:   entityID,
			})
			req.Header().Set("Authorization", authHeader)

			_, err = tokenClient.RevokeToken(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to delete token: %w", err)
			}

			fmt.Fprintf(deps.Output, "Token %q deleted successfully.\n", name)
			return nil
		},
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().String("entity-type", "user", "Entity type: user, org, workspace, resource")
	cmd.Flags().Int64("entity-id", 0, "Entity ID (defaults to current user for user type)")

	return cmd
}
