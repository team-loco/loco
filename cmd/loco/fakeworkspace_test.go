package main

import (
	"context"
	"fmt"
	"strconv"

	"connectrpc.com/connect/v2"
	"github.com/rogpeppe/go-internal/testscript"

	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/rpcerr"
)

// fakeRetentionLimit is the fake organization's limit for every signal.
const fakeRetentionLimit = 30

var errFakeRetentionRange = fmt.Errorf(
	"must be from 1 to %d days, the organization's limit, or 0 to clear it", fakeRetentionLimit,
)

// fakeRetentionFields name the UpdateWorkspace retention fields in the order fakePlatform.retention keeps them.
var fakeRetentionFields = [3]struct {
	name      string
	requested func(*workspacev1.UpdateWorkspaceRequest) *int32
}{
	{"log_retention_days", func(r *workspacev1.UpdateWorkspaceRequest) *int32 { return r.LogRetentionDays }},
	{"trace_retention_days", func(r *workspacev1.UpdateWorkspaceRequest) *int32 { return r.TraceRetentionDays }},
	{"metric_retention_days", func(r *workspacev1.UpdateWorkspaceRequest) *int32 { return r.MetricRetentionDays }},
}

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
	updated := f.platform.retention
	for i, field := range fakeRetentionFields {
		requested := field.requested(req)
		switch {
		case requested == nil:
		case *requested == 0:
			updated[i] = nil
		case *requested < 0 || *requested > fakeRetentionLimit:
			return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("%s %w", field.name, errFakeRetentionRange))
		default:
			updated[i] = new(*requested)
		}
	}
	f.platform.retention = updated
	return &workspacev1.UpdateWorkspaceResponse{WorkspaceId: req.GetWorkspaceId()}, nil
}

// workspaceRetentionDays returns the fake workspace's own days and the days that apply under the
// fake organization's limit.
func (p *fakePlatform) workspaceRetentionDays() (own, applied *workspacev1.RetentionDays) {
	var capped [3]*int32
	for i, days := range p.retention {
		capped[i] = new(int32(fakeRetentionLimit))
		if days != nil {
			capped[i] = new(min(*days, fakeRetentionLimit))
		}
	}
	return &workspacev1.RetentionDays{LogDays: p.retention[0], TraceDays: p.retention[1], MetricDays: p.retention[2]},
		&workspacev1.RetentionDays{LogDays: capped[0], TraceDays: capped[1], MetricDays: capped[2]}
}

// checkWorkspaceRetention compares the workspace's own days for logs, traces and metrics with
// args, where "-" is a signal without its own value.
func checkWorkspaceRetention(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	requireArgs(ts, neg, args, 4, "fakeapi workspace-retention <logs> <traces> <metrics>")
	api.mu.Lock()
	defer api.mu.Unlock()
	for i, days := range api.platform.retention {
		got := "-"
		if days != nil {
			got = strconv.Itoa(int(*days))
		}
		if got != args[i+1] {
			ts.Fatalf("fakeapi: workspace retention %s = %s, want %s", fakeRetentionFields[i].name, got, args[i+1])
		}
	}
}
