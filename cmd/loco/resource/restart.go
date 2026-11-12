package resource

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func newRestartCmd() *cobra.Command {
	var region string
	cmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart a service's replicas in one environment",
		Long: `Start a rolling restart using the service's current deployment.

--env or LOCO_ENV names the environment; a workspace with a single environment uses it.
Use --region to restart only one region.

Examples:
  loco resource restart web --env production
  loco resource restart web --env staging --region us-east-1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			target, err := cmdutil.ResolveTarget(ctx, cmd)
			if err != nil {
				return err
			}
			target, err = target.WithEnvironment(ctx, cmd, false)
			if err != nil {
				return err
			}
			resource, err := target.Service(ctx, args[0])
			if err != nil {
				return err
			}
			var requestedRegion *string
			if region != "" {
				requestedRegion = &region
			}
			_, err = target.Clients.Resource.RestartResource(ctx, &resourcev1.RestartResourceRequest{
				ResourceId: resource.GetId(), EnvironmentId: target.Environment.GetId(), Region: requestedRegion,
			})
			if err != nil {
				return fmt.Errorf("restart service %q: %w", args[0], err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restarting service %s in %s\n", args[0], target.Environment.GetName())
			return nil
		},
	}
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env", "", "Environment name (defaults to the workspace's only environment)")
	cmd.Flags().StringVar(&region, "region", "", "Region to restart (defaults to all regions)")
	cmd.Flags().String("host", "", "API host URL")
	return cmd
}
