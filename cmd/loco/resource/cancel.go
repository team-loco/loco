package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

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
it already runs keep it. The command lists those regions with the version each ran before.
--rollback also rolls them back, with one rollback deployment per earlier version; in a
terminal the command asks instead. A region that never ran an earlier version keeps the
canceled one. --env or LOCO_ENV names the environment; a workspace with a single environment
uses it.

Examples:
  loco resource cancel web --env production
  loco resource cancel web --env production --rollback --json`,
		Args: cobra.ExactArgs(1),
		RunE: runCancel,
	}
	cmdutil.AddWorkspaceFlags(cmd)
	cmdutil.AddEnvironmentFlag(cmd)
	cmd.Flags().Bool("rollback", false, "Also roll back the regions that already run the deployment")
	cmd.Flags().Bool("json", false, "Print the canceled deployment and the queued rollbacks as JSON")
	return cmd
}

func runCancel(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	rollback, err := cmd.Flags().GetBool("rollback")
	if err != nil {
		return fmt.Errorf("error reading rollback flag: %w", err)
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return fmt.Errorf("error reading json flag: %w", err)
	}
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
	impact, err := target.Clients.Deployment.GetDeploymentCancelImpact(
		ctx,
		&deploymentv1.GetDeploymentCancelImpactRequest{
			DeploymentId: inFlight.GetId(),
		},
	)
	if err != nil {
		return fmt.Errorf("list the regions deployment %s runs: %w", inFlight.GetId(), err)
	}
	stdout := cmd.OutOrStdout()
	restorable := restorableRegions(impact.GetFinishedRegions())
	if !asJSON {
		printFinishedRegions(stdout, inFlight.GetId(), impact.GetFinishedRegions())
	}
	if !rollback && !asJSON && len(restorable) > 0 {
		if !cmdutil.Interactive() {
			fmt.Fprintln(stdout, "Pass --rollback to roll them back.")
		} else if rollback, err = cmdutil.Ask(cmd, rollbackQuestion(restorable)); err != nil {
			return err
		}
	}
	canceled, err := target.Clients.Deployment.CancelDeployment(ctx, &deploymentv1.CancelDeploymentRequest{
		DeploymentId: inFlight.GetId(), RollbackFinishedRegions: rollback,
	})
	if err != nil {
		return fmt.Errorf("cancel deployment %s: %w", inFlight.GetId(), err)
	}
	rollbacks := canceled.GetRollbackDeploymentIds()
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(struct {
			DeploymentID          string   `json:"deploymentId"`
			RollbackDeploymentIDs []string `json:"rollbackDeploymentIds"`
		}{inFlight.GetId(), append([]string{}, rollbacks...)})
	}
	fmt.Fprintf(stdout, "Canceled deployment %s of %s in %s\n", inFlight.GetId(), args[0], target.Environment.GetName())
	for _, id := range rollbacks {
		fmt.Fprintf(stdout, "Queued rollback deployment %s\n", id)
	}
	return nil
}

// restorableRegions are the finished regions with an earlier version to roll back to.
func restorableRegions(finished []*deploymentv1.FinishedRegion) []*deploymentv1.FinishedRegion {
	var restorable []*deploymentv1.FinishedRegion
	for _, region := range finished {
		if region.GetPrevious() != nil {
			restorable = append(restorable, region)
		}
	}
	return restorable
}

func printFinishedRegions(out io.Writer, deploymentID string, finished []*deploymentv1.FinishedRegion) {
	if len(finished) == 0 {
		return
	}
	fmt.Fprintf(out, "Deployment %s already runs in:\n", deploymentID)
	for _, region := range finished {
		previous := region.GetPrevious()
		if previous == nil {
			fmt.Fprintf(out, "  %s: first deployment there, nothing to roll back to\n", region.GetRegion())
			continue
		}
		deployed := previous.GetStartedAt()
		if deployed == nil {
			deployed = previous.GetCreatedAt()
		}
		fmt.Fprintf(out, "  %s: previous version %s, deployed %s\n",
			region.GetRegion(), deploymentVersion(previous), deployed.AsTime().UTC().Format("2006-01-02 15:04 MST"))
	}
}

func rollbackQuestion(restorable []*deploymentv1.FinishedRegion) string {
	names := make([]string, 0, len(restorable))
	versions := map[string]bool{}
	for _, region := range restorable {
		names = append(names, region.GetRegion())
		versions[region.GetPrevious().GetId()] = true
	}
	to := "their previous versions"
	if len(versions) == 1 {
		to = deploymentVersion(restorable[0].GetPrevious())
	}
	return fmt.Sprintf("Also roll back %s to %s?", strings.Join(names, ", "), to)
}

// deploymentVersion names a deployment by its image tag or digest, and its commit when known.
func deploymentVersion(deployment *deploymentv1.Deployment) string {
	image := deployment.GetSpec().GetService().GetBuild().GetImage()
	reference, digest, _ := strings.Cut(image, "@")
	_, version, tagged := strings.Cut(reference[strings.LastIndex(reference, "/")+1:], ":")
	if !tagged {
		version = digest[:min(len(digest), len("sha256:")+12)]
	}
	if sha := deployment.GetCommit().GetSha(); sha != "" {
		sha = sha[:min(len(sha), 7)]
		if version == "" {
			return "commit " + sha
		}
		return version + " (commit " + sha + ")"
	}
	if version == "" {
		return deployment.GetId()
	}
	return version
}
