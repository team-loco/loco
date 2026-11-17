package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const waitUsage = "Wait until every deployment the command starts runs, and fail if one does not"

var (
	errDeploymentNotRunning = errors.New("deployment did not run")
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
		if err := waitDeployment(ctx, t, w, deployment); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Deployment %s for %s in %s is running.\n",
			deployment.GetDeploymentId(), deployment.GetService(), deployment.GetRegion()); err != nil {
			return err
		}
	}
	return nil
}

func waitDeployment(ctx context.Context, t cmdutil.Target, w io.Writer, deployment *planv1.StartedDeployment) error {
	id := deployment.GetDeploymentId()
	var cursor int64
	for {
		stream, err := t.Clients.Deployment.WatchDeployment(
			ctx,
			&deploymentv1.WatchDeploymentRequest{DeploymentId: id, AfterSeq: cursor},
		)
		if err == nil {
			for {
				event, receiveErr := stream.Receive()
				if receiveErr != nil {
					err = errors.Join(receiveErr, stream.Close())
					break
				}
				for _, entry := range event.GetActivity() {
					prefix := entry.GetReplicaName()
					if prefix != "" {
						prefix += " · "
					}
					if _, writeErr := fmt.Fprintf(w, "%s%s\n", prefix, entry.GetMessage()); writeErr != nil {
						return errors.Join(writeErr, stream.Close())
					}
					cursor = entry.GetSeq()
				}
				if event.GetHasMoreActivity() {
					continue
				}
				current := event.GetDeployment()
				switch current.GetStatus() {
				case deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING,
					deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED:
					return stream.Close()
				case deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED,
					deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED:
					outcome := "failed"
					if current.GetStatus() == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED {
						outcome = "was canceled"
					}
					problem := current.GetProblem()
					message := problem.GetMessage()
					if problem.GetDetail() != "" {
						message += " · " + problem.GetDetail()
					}
					return errors.Join(
						fmt.Errorf(
							"%w: deployment %s for %s in %s %s: %s",
							errDeploymentNotRunning,
							id,
							deployment.GetService(),
							deployment.GetRegion(),
							outcome,
							message,
						),
						stream.Close(),
					)
				}
			}
		}
		if ctx.Err() != nil {
			return fmt.Errorf("watch deployment %s: %w", id, ctx.Err())
		}
		if !errors.Is(err, io.EOF) && connect.CodeOf(err) != connect.CodeUnavailable &&
			connect.CodeOf(err) != connect.CodeDeadlineExceeded {
			return fmt.Errorf("watch deployment %s: %w", id, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
