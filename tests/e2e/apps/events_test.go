package apps

import (
	"context"
	"testing"
	"time"

	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestWorkspaceEventStream(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	sibling := w.NewWorkspace(t, "stream-sibling")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	request := &eventv1.StreamEventsRequest{OrgId: w.OrgID, WorkspaceId: w.ID, Query: "@type:workspace.updated"}
	stream, err := p.User.Event.StreamEvents(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []string{sibling.ID, w.ID} {
		if _, err = p.User.Workspace.UpdateWorkspace(
			ctx,
			&workspacev1.UpdateWorkspaceRequest{WorkspaceId: workspace, Name: new("stream-updated")},
		); err != nil {
			t.Fatal(err)
		}
	}
	first, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetEvents()) != 1 || first.GetEvents()[0].GetWorkspaceId() != w.ID {
		t.Fatalf("batch = %v", first)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	request.Cursor = first.GetCursor()
	if _, err = p.User.Workspace.UpdateWorkspace(
		ctx,
		&workspacev1.UpdateWorkspaceRequest{WorkspaceId: w.ID, Name: new("stream-resumed")},
	); err != nil {
		t.Fatal(err)
	}
	resumed, err := p.User.Event.StreamEvents(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	next, err := resumed.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if len(next.GetEvents()) != 1 || next.GetEvents()[0].GetSeq() <= first.GetEvents()[0].GetSeq() {
		t.Fatalf("resumed = %v", next)
	}
}
