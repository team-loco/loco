package infra

import (
	"context"
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/ui"
)

const transferTimeout = 30 * time.Second

func buildTransferCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transfer <service> --to <partial>",
		Short: "Move a service to another partial",
		Long: `Move a service of the current workspace to another partial, so that the loco.yaml
declaring that partial owns it from then on.

Examples:
  loco infra transfer api --to platform`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			partial, err := cmd.Flags().GetString("to")
			if err != nil {
				return fmt.Errorf("error reading to flag: %w", err)
			}
			return runTransfer(cmd, args[0], partial)
		},
	}
	addWorkspaceFlags(cmd)
	cmd.Flags().String("to", "", "Partial that owns the service after the transfer")
	if err := cmd.MarkFlagRequired("to"); err != nil {
		panic(err)
	}
	return cmd
}

func runTransfer(cmd *cobra.Command, name, partial string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), transferTimeout)
	defer cancel()

	t, err := resolveWorkspaceTarget(ctx, cmd)
	if err != nil {
		return err
	}
	resources := t.resourceClient()

	getReq := connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{
			NameKey: &resourcev1.GetResourceNameKey{WorkspaceId: t.workspaceID, Name: name},
		},
	})
	getReq.Header().Set("Authorization", t.authHeader)
	found, err := resources.GetResource(ctx, getReq)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", name, err)
	}
	resourceID := found.Msg.GetResource().GetId()

	transferReq := connect.NewRequest(&resourcev1.TransferPartialRequest{ResourceId: resourceID, Partial: partial})
	transferReq.Header().Set("Authorization", t.authHeader)
	if _, transferErr := resources.TransferPartial(ctx, transferReq); transferErr != nil {
		return fmt.Errorf("transfer %s to partial %s: %w", name, partial, transferErr)
	}

	done := lipgloss.NewStyle().Foreground(ui.Ok).Bold(true).Render("Moved")
	_, err = lipgloss.Printf("%s %s to partial %s.\n", done, name, partial)
	return err
}
