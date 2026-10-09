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

var errFakeUnknownEnvironment = errors.New("fakeapi: unknown environment")

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
