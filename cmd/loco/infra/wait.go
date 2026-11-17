package infra

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const waitUsage = "Wait until every deployment the command starts runs, and fail if one does not"

var (
	errDeploymentNotRunning = errors.New("deployment did not run")
	errWatchEnded           = errors.New("the deployment watch ended before the deployment ran")
)

func addWaitFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("wait", false, waitUsage)
}

func readWait(cmd *cobra.Command) (bool, error) {
	wait, err := cmd.Flags().GetBool("wait")
	if err != nil {
		return false, fmt.Errorf("error reading wait flag: %w", err)
	}
	return wait, nil
}

// waitRunning returns once every deployment runs, and on the first one that fails or is canceled.
func waitRunning(ctx context.Context, t cmdutil.Target, w io.Writer, deployments []*planv1.StartedDeployment) error {
	for _, deployment := range deployments {
		if err := waitDeployment(ctx, t, deployment); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Deployment %s for %s in %s is running.\n",
			deployment.GetDeploymentId(), deployment.GetService(), deployment.GetRegion()); err != nil {
			return err
		}
	}
	return nil
}

func waitDeployment(ctx context.Context, t cmdutil.Target, deployment *planv1.StartedDeployment) error {
	id := deployment.GetDeploymentId()
	stream, err := t.Clients.Deployment.WatchDeployment(ctx, &deploymentv1.WatchDeploymentRequest{DeploymentId: id})
	if err != nil {
		return fmt.Errorf("watch deployment %s: %w", id, err)
	}
	for {
		event, receiveErr := stream.Receive()
		if receiveErr != nil {
			closeErr := stream.Close()
			if errors.Is(receiveErr, io.EOF) {
				return errors.Join(fmt.Errorf("%w: %s", errWatchEnded, id), closeErr)
			}
			return errors.Join(fmt.Errorf("watch deployment %s: %w", id, receiveErr), closeErr)
		}
		var outcome string
		switch event.GetStatus() {
		case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
			deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_SUCCEEDED:
			return stream.Close()
		case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED:
			outcome = "failed"
		case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_CANCELED:
			outcome = "was canceled"
		default:
			continue
		}
		closeErr := stream.Close()
		return errors.Join(fmt.Errorf("%w: deployment %s for %s in %s %s: %s", errDeploymentNotRunning,
			id, deployment.GetService(), deployment.GetRegion(), outcome, event.GetMessage()), closeErr)
	}
}
