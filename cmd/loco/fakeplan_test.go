package loco

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const fakePlanRevision = 7

var (
	errFakeUnknownEnvironment = errors.New("fakeapi: unknown environment")
	errFakeApplyRefused       = errors.New("fakeapi: apply refused")
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
	revision := plan.GetRevision()
	if f.platform.revisionMoves {
		revision++
	}
	if req.Msg.GetRevision() != revision {
		return nil, fakeApplyRefused(&planv1.ApplyRefusal{Revision: revision})
	}
	var unconfirmed []*planv1.PlanOperation
	var deployments []*planv1.StartedDeployment
	for _, op := range plan.GetOperations() {
		isImport := op.GetKind() == planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT
		if (op.GetDestructive() && !req.Msg.GetConfirmDestructive()) || (isImport && !req.Msg.GetConfirmImport()) {
			unconfirmed = append(unconfirmed, op)
			continue
		}
		if op.GetKind() != planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE && !op.GetNeedsDeploy() {
			deployments = append(deployments, &planv1.StartedDeployment{
				Service:      op.GetService(),
				Region:       fakeDefaultRegion,
				DeploymentId: "dep-" + op.GetService(),
			})
		}
	}
	if len(unconfirmed) > 0 {
		return nil, fakeApplyRefused(&planv1.ApplyRefusal{Unconfirmed: unconfirmed})
	}
	f.platform.applies = append(f.platform.applies, req.Msg)
	return connect.NewResponse(&planv1.ApplyResponse{
		Revision:    revision + 1,
		Operations:  plan.GetOperations(),
		Deployments: deployments,
	}), nil
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
