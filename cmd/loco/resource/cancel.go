package resource

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

var errNothingToCancel = errors.New("no deployment is building or rolling out")

func newCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <name>",
		Short: "Cancel a service's deployment that is building or rolling out",
		Long: `Cancel the newest deployment of a service that is still building or rolling out in one environment.

Regions the deployment does not run yet go back to the version that served them before; regions
it already runs keep it. --env or LOCO_ENV names the environment; a workspace with a single
environment uses it.

Examples:
  loco resource cancel web --env production`,
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
			listed, err := target.Clients.Deployment.ListDeployments(ctx, &deploymentv1.ListDeploymentsRequest{
				ResourceId: resource.GetId(), EnvironmentId: target.Environment.GetId(),
			})
			if err != nil {
				return fmt.Errorf("list deployments of %q: %w", args[0], err)
			}
			var inFlight *deploymentv1.Deployment
			for _, deployment := range listed.GetDeployments() {
				status := deployment.GetStatus()
				if status == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_BUILDING ||
					status == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEPLOYING {
					inFlight = deployment
					break
				}
			}
			if inFlight == nil {
				return fmt.Errorf("%w for %s in %s", errNothingToCancel, args[0], target.Environment.GetName())
			}
			if _, err = target.Clients.Deployment.CancelDeployment(ctx, &deploymentv1.CancelDeploymentRequest{
				DeploymentId: inFlight.GetId(),
			}); err != nil {
				return fmt.Errorf("cancel deployment %s: %w", inFlight.GetId(), err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Canceled deployment %s of %s in %s\n",
				inFlight.GetId(), args[0], target.Environment.GetName())
			return nil
		},
	}
	cmdutil.AddWorkspaceFlags(cmd)
	cmdutil.AddEnvironmentFlag(cmd)
	return cmd
}
