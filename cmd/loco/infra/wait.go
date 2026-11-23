package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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

// waitRunning returns once every deployment runs or a newer one replaced it, and on the first one
// that fails or a user cancels.
func waitRunning(ctx context.Context, t cmdutil.Target, w io.Writer, deployments []*planv1.StartedDeployment) error {
	for _, deployment := range deployments {
		if err := waitDeployment(ctx, t, w, deployment); err != nil {
			return err
		}
	}
	return nil
}

func waitDeployment(ctx context.Context, t cmdutil.Target, w io.Writer, deployment *planv1.StartedDeployment) error {
	id := deployment.GetDeploymentId()
	var cursor int64
	states := map[string]deploymentv1.PlacementStatus{}
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
					var prefix strings.Builder
					for _, part := range []string{entry.GetRegion(), entry.GetReplicaName()} {
						if part != "" {
							prefix.WriteString(part + " · ")
						}
					}
					if _, writeErr := fmt.Fprintf(w, "%s%s\n", prefix.String(), entry.GetMessage()); writeErr != nil {
						return errors.Join(writeErr, stream.Close())
					}
					cursor = entry.GetSeq()
				}
				if writeErr := writePlacementChanges(w, states, event.GetDeployment()); writeErr != nil {
					return errors.Join(writeErr, stream.Close())
				}
				if event.GetHasMoreActivity() {
					continue
				}
				current := event.GetDeployment()
				switch current.GetStatus() {
				case deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING,
					deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED:
					// A deployment runs before it starts while its replicas are ready but not yet available.
					if current.GetStartedAt() == nil {
						continue
					}
					_, writeErr := fmt.Fprintf(w, "Deployment %s for %s in %s is running.\n",
						id, deployment.GetService(), regionList(deployment))
					return errors.Join(writeErr, stream.Close())
				case deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED,
					deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CRASHED,
					deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED:
					outcome := "failed"
					if current.GetStatus() == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CRASHED {
						outcome = "crashed"
					}
					if current.GetStatus() == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED {
						outcome = "was canceled"
						if by := current.GetCanceledByName(); by != "" {
							outcome += " by " + by
						} else if newer := supersededBy(current); newer != "" {
							_, writeErr := fmt.Fprintf(w, "Deployment %s for %s in %s was superseded by %s.\n",
								id, deployment.GetService(), regionList(deployment), newer)
							return errors.Join(writeErr, stream.Close())
						}
					}
					return errors.Join(
						fmt.Errorf(
							"%w: deployment %s for %s in %s %s%s",
							errDeploymentNotRunning,
							id,
							deployment.GetService(),
							regionList(deployment),
							outcome,
							placementProblems(current),
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

// writePlacementChanges prints one line for each region whose placement status changed.
func writePlacementChanges(
	w io.Writer,
	states map[string]deploymentv1.PlacementStatus,
	deployment *deploymentv1.Deployment,
) error {
	for _, placement := range deployment.GetPlacements() {
		region, status := placement.GetRegion(), placement.GetStatus()
		if status == deploymentv1.PlacementStatus_PLACEMENT_STATUS_UNSPECIFIED || states[region] == status {
			continue
		}
		states[region] = status
		label := strings.ToLower(strings.TrimPrefix(status.String(), "PLACEMENT_STATUS_"))
		if status == deploymentv1.PlacementStatus_PLACEMENT_STATUS_PENDING {
			label = "waiting for the build"
		}
		if _, err := fmt.Fprintf(w, "%s · %s\n", region, label); err != nil {
			return err
		}
	}
	return nil
}

// supersededBy is the newer deployment that replaced one of the deployment's regions, if any.
func supersededBy(deployment *deploymentv1.Deployment) string {
	for _, placement := range deployment.GetPlacements() {
		if placement.GetSupersededBy() != "" {
			return placement.GetSupersededBy()
		}
	}
	return ""
}

func regionList(deployment *planv1.StartedDeployment) string {
	return strings.Join(deployment.GetRegions(), ", ")
}

// placementProblems lists the problem of each region that reports one, naming the region when
// the deployment has several.
func placementProblems(deployment *deploymentv1.Deployment) string {
	var problems []string
	for _, placement := range deployment.GetPlacements() {
		problem := placement.GetProblem()
		if problem == nil {
			continue
		}
		message := problem.GetMessage()
		if len(deployment.GetPlacements()) > 1 {
			message = placement.GetRegion() + ": " + message
		}
		if problem.GetDetail() != "" {
			message += " · " + problem.GetDetail()
		}
		problems = append(problems, message)
	}
	if len(problems) == 0 {
		return ""
	}
	return ": " + strings.Join(problems, "; ")
}
