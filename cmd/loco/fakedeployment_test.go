package main

import (
	"context"

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
	f.mu.Unlock()
	if !ok {
		outcome = deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING
	}
	deployment := &deploymentv1.Deployment{
		Id:     req.GetDeploymentId(),
		Status: outcome,
	}
	if req.GetAfterSeq() < 1 {
		if err := stream.Send(
			&deploymentv1.WatchDeploymentResponse{
				Deployment:      deployment,
				HasMoreActivity: true,
				Activity: []*deploymentv1.Activity{
					{Seq: 1, ReplicaName: req.GetDeploymentId(), Message: "Starting"},
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
	if req.GetAfterSeq() < 2 {
		activity = []*deploymentv1.Activity{{Seq: 2, ReplicaName: req.GetDeploymentId(), Message: "Serving traffic"}}
	}
	if req.GetAfterSeq() < 3 {
		activity = append(activity, &deploymentv1.Activity{Seq: 3, Message: "Scaled to 3 replicas"})
	}
	return stream.Send(&deploymentv1.WatchDeploymentResponse{Deployment: deployment, Activity: activity})
}
