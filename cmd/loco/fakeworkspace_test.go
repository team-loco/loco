package main

import (
	"cmp"
	"context"
	"slices"

	"connectrpc.com/connect/v2"
	"github.com/rogpeppe/go-internal/testscript"
	"google.golang.org/protobuf/proto"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
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
	if req.Name != nil {
		for _, ws := range f.platform.workspaces {
			if ws.GetId() == req.GetWorkspaceId() {
				ws.Name = req.GetName()
			}
		}
	}
	own := f.platform.retention
	setFakeRetention(&own.LogDays, req.LogRetentionDays)
	setFakeRetention(&own.TraceDays, req.TraceRetentionDays)
	setFakeRetention(&own.MetricDays, req.MetricRetentionDays)
	return &workspacev1.UpdateWorkspaceResponse{WorkspaceId: req.GetWorkspaceId()}, nil
}

func (s *fakeWorkspaceService) CreateWorkspace(
	ctx context.Context,
	req *workspacev1.CreateWorkspaceRequest,
) (*workspacev1.CreateWorkspaceResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: workspacev1connect.WorkspaceServiceCreateWorkspaceProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ws := &workspacev1.Workspace{Id: "new-" + req.GetName(), OrgId: req.GetOrgId(), Name: req.GetName()}
	f.platform.workspaces = append(f.platform.workspaces, ws)
	return &workspacev1.CreateWorkspaceResponse{WorkspaceId: ws.GetId()}, nil
}

func (s *fakeWorkspaceService) DeleteWorkspace(
	ctx context.Context,
	req *workspacev1.DeleteWorkspaceRequest,
) (*workspacev1.DeleteWorkspaceResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: workspacev1connect.WorkspaceServiceDeleteWorkspaceProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.platform.workspaces = slices.DeleteFunc(f.platform.workspaces, func(ws *workspacev1.Workspace) bool {
		return ws.GetId() == req.GetWorkspaceId()
	})
	return &workspacev1.DeleteWorkspaceResponse{}, nil
}

// addFakeScope adds an organization, or a workspace in the fake organization.
func addFakeScope(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	requireArgs(ts, neg, args, 3, "fakeapi org|workspace <id> <name>")
	api.mu.Lock()
	defer api.mu.Unlock()
	if args[0] == "org" {
		api.platform.orgs = append(api.platform.orgs, &orgv1.Organization{Id: args[1], Name: args[2]})
		return
	}
	ws := &workspacev1.Workspace{Id: args[1], OrgId: fakeOrgID, OrgName: fakeOrgName, Name: args[2]}
	api.platform.workspaces = append(api.platform.workspaces, ws)
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
