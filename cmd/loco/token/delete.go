package token

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/token/internal"
	"github.com/team-loco/loco/internal/ui"
	tokenv1 "github.com/team-loco/loco/shared/proto/token/v1"
	userv1 "github.com/team-loco/loco/shared/proto/user/v1"
)

type deleteRunner struct {
	deps *internal.TokenDeps
	name string
}

func buildDeleteCmd(deps *internal.TokenDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an API token",
		Long:  "Delete (revoke) a personal access token by name.",
		Args:  cobra.ExactArgs(1),
	}

	cmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	if deps != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			runner := &deleteRunner{deps: deps, name: args[0]}
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *deleteRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Get current user
	whoAmIReq := connect.NewRequest(&userv1.WhoAmIRequest{})
	whoAmIReq.Header().Set("Authorization", r.deps.AuthHeader())

	whoAmIResp, err := r.deps.WhoAmI(ctx, whoAmIReq)
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	force, _ := cmd.Flags().GetBool("force")
	if !force {
		confirm, err := ui.AskYesNo(fmt.Sprintf("Are you sure you want to delete token %q? This cannot be undone.", r.name))
		if err != nil {
			return fmt.Errorf("failed to prompt for confirmation: %w", err)
		}
		if !confirm {
			fmt.Fprintln(r.deps.Stdout, "Aborted.")
			return nil
		}
	}

	req := connect.NewRequest(&tokenv1.RevokeTokenRequest{
		Name:       r.name,
		EntityType: tokenv1.EntityType_ENTITY_TYPE_USER,
		EntityId:   whoAmIResp.Msg.User.Id,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	_, err = r.deps.RevokeToken(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete token: %w", err)
	}

	fmt.Fprintf(r.deps.Stdout, "Token %q deleted successfully.\n", r.name)

	return nil
}
