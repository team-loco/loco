package resource

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

type destroyDeps struct {
	Stdout io.Writer
}

func buildDestroyCmd() *cobra.Command {
	deps := destroyDeps{Stdout: os.Stdout}
	return newDestroyCmd(deps)
}

func newDestroyCmd(deps destroyDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy <name>",
		Short: "Destroy a service",
		Long: `Destroy a service and all its resources.

Examples:
  loco resource destroy myapp
  loco resource destroy myapp --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return fmt.Errorf("error reading yes flag: %w", err)
			}

			// Confirmation prompt (before any API calls)
			if !yes {
				confirmed, confirmErr := ui.AskYesNo(
					fmt.Sprintf("Are you sure you want to destroy the service '%s'?", name),
				)
				if confirmErr != nil {
					return confirmErr
				}
				if !confirmed {
					fmt.Fprintln(deps.Stdout, "Aborted.")
					return nil
				}
			}

			t, err := cmdutil.ResolveTarget(ctx, cmd)
			if err != nil {
				return err
			}
			resource, err := t.Service(ctx, name)
			if err != nil {
				return err
			}
			slog.Debug("destroying service", "resource_id", resource.GetId(), "name", name)

			deleteReq := &resourcev1.DeleteResourceRequest{
				ResourceId: resource.GetId(),
			}

			_, err = t.Clients.Resource.DeleteResource(ctx, deleteReq)
			if err != nil {
				return fmt.Errorf("failed to destroy service '%s': %w", name, err)
			}

			successMsg := fmt.Sprintf("\n🎉 Service '%s' destroyed!", name)
			s := lipgloss.NewStyle().
				Bold(true).
				Foreground(ui.Ok).
				Render(successMsg)

			fmt.Fprintln(deps.Stdout, s)

			return nil
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}
