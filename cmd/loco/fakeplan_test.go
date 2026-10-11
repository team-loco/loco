package loco

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"

	"connectrpc.com/connect"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	fakePlanRevision = 7
	fakeMovedSuffix  = "-moved"
)

var (
	errFakeUnknownEnvironment = errors.New("fakeapi: unknown environment")
	errFakeApplyRefused       = errors.New("fakeapi: apply refused")
	errFakeBuildNotSucceeded  = errors.New("fakeapi: the build has not succeeded")
	errFakeProvisionMixed     = errors.New("fakeapi: an apply provisions services or deploys builds, not both")
	errFakeNotProvisionable   = errors.New("fakeapi: the plan does not create the service from source")
	errFakeClusterHeld        = errors.New(
		"each environment needs its own cluster, and this cluster already runs the service for another environment",
	)
)

type fakePlanService struct {
	planv1connect.UnimplementedPlanServiceHandler

	api *fakeAPI
}

func (f *fakeAPI) registerPlan(mux *http.ServeMux) {
	mux.Handle(planv1connect.NewPlanServiceHandler(&fakePlanService{api: f}))
}

func (p *fakePlatform) setPlan(data string) error {
	plan := &planv1.PlanResponse{}
	if err := protojson.Unmarshal([]byte(data), plan); err != nil {
		return err
	}
	p.plan = plan
	return nil
}

func (p *fakePlatform) currentPlan() *planv1.PlanResponse {
	if p.plan == nil {
		return &planv1.PlanResponse{Revision: fakePlanRevision}
	}
	plan, ok := proto.Clone(p.plan).(*planv1.PlanResponse)
	if !ok {
		panic("fakeapi: plan clone is not a PlanResponse")
	}
	return plan
}

func (s *fakePlanService) Plan(
	_ context.Context,
	req *connect.Request[planv1.PlanRequest],
) (*connect.Response[planv1.PlanResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	if req.Msg.GetEnvironmentId() != fakeEnvironmentID {
		return nil, connect.NewError(connect.CodeNotFound, errFakeUnknownEnvironment)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.platform.planned = append(f.platform.planned, req.Msg.GetFile())
	return connect.NewResponse(f.platform.currentPlan()), nil
}

func (s *fakePlanService) Apply(
	_ context.Context,
	req *connect.Request[planv1.ApplyRequest],
) (*connect.Response[planv1.ApplyResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	if req.Msg.GetEnvironmentId() != fakeEnvironmentID {
		return nil, connect.NewError(connect.CodeNotFound, errFakeUnknownEnvironment)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	plan := f.platform.currentPlan()
	if len(plan.GetErrors()) > 0 {
		return nil, fakeApplyRefused(&planv1.ApplyRefusal{Errors: plan.GetErrors()})
	}
	revision := plan.GetRevision() + int64(len(f.platform.applies))
	if f.platform.revisionMoves {
		revision++
	}
	if req.Msg.GetRevision() != revision {
		return nil, fakeApplyRefused(&planv1.ApplyRefusal{Revision: revision})
	}
	images := f.platform.currentImages(plan)
	if !maps.Equal(req.Msg.GetImages(), images) {
		return nil, fakeApplyRefused(&planv1.ApplyRefusal{Images: images})
	}
	if len(req.Msg.GetProvision()) > 0 {
		return f.provision(req.Msg, plan, revision)
	}
	if f.platform.clusterHeld {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errFakeClusterHeld)
	}
	builds := req.Msg.GetBuilds()
	for _, service := range slices.Sorted(maps.Keys(builds)) {
		build := f.findBuild(builds[service])
		if build == nil || build.GetStatus() != buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errFakeBuildNotSucceeded)
		}
	}
	var unconfirmed []*planv1.PlanOperation
	var deployments []*planv1.StartedDeployment
	deployed := map[string]bool{}
	for _, op := range plan.GetOperations() {
		isImport := op.GetKind() == planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT
		if (op.GetDestructive() && !req.Msg.GetConfirmDestructive()) || (isImport && !req.Msg.GetConfirmImport()) {
			unconfirmed = append(unconfirmed, op)
			continue
		}
		if op.GetKind() == planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE {
			continue
		}
		if op.GetKind() == planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE {
			f.platform.addResource(op.GetService())
		}
		if op.GetNeedsDeploy() && builds[op.GetService()] == "" {
			continue
		}
		deployed[op.GetService()] = true
		deployments = append(deployments, fakeStartedDeployment(op.GetService()))
	}
	if len(unconfirmed) > 0 {
		return nil, fakeApplyRefused(&planv1.ApplyRefusal{Unconfirmed: unconfirmed})
	}
	for _, service := range slices.Sorted(maps.Keys(builds)) {
		if !deployed[service] {
			deployments = append(deployments, fakeStartedDeployment(service))
		}
	}
	f.platform.applies = append(f.platform.applies, req.Msg)
	return connect.NewResponse(&planv1.ApplyResponse{
		Revision:    revision + 1,
		Operations:  plan.GetOperations(),
		Deployments: deployments,
	}), nil
}

func (p *fakePlatform) currentImages(plan *planv1.PlanResponse) map[string]string {
	images := maps.Clone(plan.GetImages())
	if !p.imagesMove {
		return images
	}
	for reference, pinned := range images {
		images[reference] = pinned + fakeMovedSuffix
	}
	return images
}

func (f *fakeAPI) provision(
	req *planv1.ApplyRequest,
	plan *planv1.PlanResponse,
	revision int64,
) (*connect.Response[planv1.ApplyResponse], error) {
	if len(req.GetBuilds()) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errFakeProvisionMixed)
	}
	var operations []*planv1.PlanOperation
	for _, service := range req.GetProvision() {
		index := slices.IndexFunc(plan.GetOperations(), func(op *planv1.PlanOperation) bool {
			return op.GetService() == service &&
				op.GetKind() == planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE && op.GetNeedsDeploy()
		})
		if index < 0 {
			refused := &planv1.PlanError{Service: service, Message: errFakeNotProvisionable.Error()}
			return nil, fakeApplyRefused(&planv1.ApplyRefusal{Errors: []*planv1.PlanError{refused}})
		}
		operations = append(operations, plan.GetOperations()[index])
		f.platform.addResource(service)
	}
	f.platform.applies = append(f.platform.applies, req)
	return connect.NewResponse(&planv1.ApplyResponse{Revision: revision + 1, Operations: operations}), nil
}

func fakeStartedDeployment(service string) *planv1.StartedDeployment {
	return &planv1.StartedDeployment{Service: service, Region: fakeDefaultRegion, DeploymentId: "dep-" + service}
}

func fakeApplyRefused(refusal *planv1.ApplyRefusal) error {
	refused := connect.NewError(connect.CodeFailedPrecondition, errFakeApplyRefused)
	detail, err := connect.NewErrorDetail(refusal)
	if err != nil {
		panic(err)
	}
	refused.AddDetail(detail)
	return refused
}
