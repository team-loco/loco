package main

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
)

const fakeFailedMessage = "replicas crash on start"

type fakeDeploymentService struct {
	deploymentv1connect.UnimplementedDeploymentServiceHandler

	api *fakeAPI
}

func (s *fakeDeploymentService) WatchDeployment(
	ctx context.Context,
	req *deploymentv1.WatchDeploymentRequest,
	stream deploymentv1connect.DeploymentServiceWatchDeploymentServerStream,
) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: deploymentv1connect.DeploymentServiceWatchDeploymentProcedure},
		info.RequestHeader(),
	); err != nil {
		return err
	}
	f.mu.Lock()
	outcome, ok := f.platform.deploymentOutcomes[req.GetDeploymentId()]
	building := false
	if len(f.platform.applies) > 0 {
		apply := f.platform.applies[len(f.platform.applies)-1]
		building = apply.GetBuilds()[strings.TrimPrefix(req.GetDeploymentId(), "dep-")] != ""
	}
	buildOutcome := f.platform.outcome
	f.mu.Unlock()
	if !ok {
		outcome = deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING
	}
	deployment := &deploymentv1.Deployment{
		Id:     req.GetDeploymentId(),
		Status: outcome,
	}
	offset := int64(0)
	if building {
		offset = 4
		messages := []string{"Fetching source", "Building image", "Pushing image", "Build succeeded"}
		if buildOutcome == outcomeFailed {
			messages = []string{"Fetching source", "Building image", "Build failed · RUN go build exited with code 1"}
		}
		if buildOutcome == outcomeRunning {
			messages = messages[:1]
		}
		deployment.Status = deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_BUILDING
		var activity []*deploymentv1.Activity
		for i, message := range messages {
			if seq := int64(i + 1); seq > req.GetAfterSeq() {
				activity = append(activity, &deploymentv1.Activity{Seq: seq, Message: message})
			}
		}
		if buildOutcome == outcomeFailed {
			deployment.Status = deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED
			deployment.Problem = &deploymentv1.Problem{
				Reason:  "build_failed",
				Message: "The build failed.",
				Detail:  "RUN go build exited with code 1",
			}
		}
		if err := stream.Send(
			&deploymentv1.WatchDeploymentResponse{
				Deployment:      deployment,
				Activity:        activity,
				HasMoreActivity: buildOutcome == outcomeSucceeded,
			},
		); err != nil {
			return err
		}
		if buildOutcome == outcomeFailed {
			return nil
		}
		if buildOutcome == outcomeRunning {
			<-ctx.Done()
			return ctx.Err()
		}
	}
	if req.GetAfterSeq() < offset+1 {
		if err := stream.Send(
			&deploymentv1.WatchDeploymentResponse{
				Deployment:      deployment,
				HasMoreActivity: true,
				Activity: []*deploymentv1.Activity{
					{Seq: offset + 1, ReplicaName: req.GetDeploymentId(), Message: "Starting"},
				},
			},
		); err != nil {
			return err
		}
		if outcome == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEPLOYING {
			return connect.NewError(connect.CodeUnavailable, "stream dropped")
		}
	}
	if outcome == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEPLOYING {
		outcome = deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING
	}
	deployment.Status = outcome
	if outcome == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED {
		deployment.Problem = &deploymentv1.Problem{Message: fakeFailedMessage, Detail: "exit 1"}
	}
	var activity []*deploymentv1.Activity
	if req.GetAfterSeq() < offset+2 {
		activity = []*deploymentv1.Activity{
			{Seq: offset + 2, ReplicaName: req.GetDeploymentId(), Message: "Serving traffic"},
		}
	}
	if req.GetAfterSeq() < offset+3 {
		activity = append(activity, &deploymentv1.Activity{Seq: offset + 3, Message: "Scaled to 3 replicas"})
	}
	return stream.Send(&deploymentv1.WatchDeploymentResponse{Deployment: deployment, Activity: activity})
}
