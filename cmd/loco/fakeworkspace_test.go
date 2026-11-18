package main

import (
	"cmp"
	"context"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

// fakeRetentionLimit is the fake organization's limit for every signal.
const fakeRetentionLimit = 30

func (s *fakeWorkspaceService) UpdateWorkspace(
	ctx context.Context,
	req *workspacev1.UpdateWorkspaceRequest,
) (*workspacev1.UpdateWorkspaceResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: workspacev1connect.WorkspaceServiceUpdateWorkspaceProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	own := f.platform.retention
	setFakeRetention(&own.LogDays, req.LogRetentionDays)
	setFakeRetention(&own.TraceDays, req.TraceRetentionDays)
	setFakeRetention(&own.MetricDays, req.MetricRetentionDays)
	return &workspacev1.UpdateWorkspaceResponse{WorkspaceId: req.GetWorkspaceId()}, nil
}

// setFakeRetention applies one requested signal: nil keeps it and 0 clears it.
func setFakeRetention(own **int32, requested *int32) {
	switch {
	case requested == nil:
	case *requested == 0:
		*own = nil
	default:
		*own = requested
	}
}

// workspaceRetention returns the fake workspace's own days and the days that apply, where a
// signal without its own value gets the fake organization's limit.
func (p *fakePlatform) workspaceRetention() (own, applied *workspacev1.RetentionDays) {
	own = proto.CloneOf(p.retention)
	orLimit := func(days int32) *int32 { return new(cmp.Or(days, fakeRetentionLimit)) }
	return own, &workspacev1.RetentionDays{
		LogDays:    orLimit(own.GetLogDays()),
		TraceDays:  orLimit(own.GetTraceDays()),
		MetricDays: orLimit(own.GetMetricDays()),
	}
}
