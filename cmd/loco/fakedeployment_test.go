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

// WatchDeployment streams pending, deploying and the outcome set for the deployment, running by
// default.
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
		outcome = deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING
	}
	events := []*deploymentv1.WatchDeploymentResponse{
		{DeploymentId: req.GetDeploymentId(), Status: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING},
		{DeploymentId: req.GetDeploymentId(), Status: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_DEPLOYING},
	}
	final := &deploymentv1.WatchDeploymentResponse{DeploymentId: req.GetDeploymentId(), Status: outcome}
	if outcome == deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED {
		final.Message = fakeFailedMessage
	}
	if outcome != deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_DEPLOYING {
		events = append(events, final)
	}
	for _, event := range events {
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return nil
}
