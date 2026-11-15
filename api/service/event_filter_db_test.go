package service

import (
	"slices"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func TestListEventsRequestIDAndActors(t *testing.T) {
	t.Parallel()
	f := newScopedAuditFixture(t)
	principal, _ := reqctx.PrincipalFrom(f.owner)
	orgID := uuid.MustParse(f.orgID)
	tokenID := uuid.New()
	var primary []int64
	requestIDs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for _, workspace := range []uuid.UUID{f.workspaceID, f.otherWorkspaceID} {
		actors := []string{
			string(genDb.EntityTypeUser), string(genDb.EntityTypeWorkspace),
			events.ActorSystem, "agent", string(genDb.EntityTypeUser),
		}
		for i, actor := range actors {
			id := &principal.Entity.ID
			if actor == string(genDb.EntityTypeWorkspace) {
				id = &tokenID
			}
			if actor != string(genDb.EntityTypeUser) && actor != string(genDb.EntityTypeWorkspace) {
				id = nil
			}
			seq, err := f.queries.InsertEvent(t.Context(), genDb.InsertEventParams{
				Type:        events.TokenCreated,
				OrgID:       &orgID,
				WorkspaceID: &workspace,
				ResourceID:  &f.resourceID,
				ActorType:   actor,
				ActorID:     id,
				RequestID:   new(requestIDs[i]),
				Data:        []byte(`{}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if workspace == f.workspaceID {
				primary = append(primary, seq)
			}
		}
	}
	for _, tc := range []struct {
		name      string
		ids       []string
		types     []string
		requestID string
		want      []int64
	}{
		{name: "person", ids: []string{principal.Entity.ID.String()}, want: []int64{primary[4], primary[0]}},
		{name: "token", ids: []string{tokenID.String()}, want: []int64{primary[1]}},
		{name: "Loco", types: []string{events.ActorSystem}, want: []int64{primary[2]}},
		{name: "cluster agent", types: []string{"agent"}, want: []int64{primary[3]}},
		{name: "person and Loco", ids: []string{principal.Entity.ID.String()},
			types: []string{events.ActorSystem}, want: []int64{primary[4], primary[2], primary[0]}},
		{name: "request ID", requestID: requestIDs[1], want: []int64{primary[1]}},
		{name: "combined", ids: []string{tokenID.String()}, requestID: requestIDs[1], want: []int64{primary[1]}},
		{name: "different request", requestID: uuid.NewString()},
		{name: "different actor", ids: []string{uuid.NewString()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, resource := range []string{"", f.resourceID.String()} {
				req := &eventv1.ListEventsRequest{
					OrgId:       f.orgID,
					WorkspaceId: f.workspaceID.String(),
					ResourceId:  resource,
					Types:       []string{events.TokenCreated},
					ActorIds:    tc.ids,
					ActorTypes:  tc.types,
					RequestId:   tc.requestID,
					PageSize:    1,
				}
				var got []int64
				for {
					res, err := f.events.ListEvents(f.owner, req)
					if err != nil {
						t.Fatal(err)
					}
					for _, e := range res.GetEvents() {
						got = append(got, e.GetSeq())
					}
					if res.GetNextBeforeSeq() == 0 {
						break
					}
					req.BeforeSeq = res.GetNextBeforeSeq()
				}
				if !slices.Equal(got, tc.want) {
					t.Fatalf("resource=%s: seqs=%v want %v", resource, got, tc.want)
				}
			}
		})
	}
	req := &eventv1.ListEventsRequest{
		OrgId:     f.orgID,
		Types:     []string{events.TokenCreated},
		RequestId: requestIDs[0],
		ActorIds:  []string{principal.Entity.ID.String()},
	}
	orgResult, err := f.events.ListEvents(f.owner, req)
	if err != nil || len(orgResult.GetEvents()) != 2 {
		t.Fatalf("org search=%v %v", orgResult, err)
	}
	admin := auditScope(t, genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeAdmin)
	for _, workspace := range []string{"", f.otherWorkspaceID.String()} {
		req.WorkspaceId = workspace
		if _, scopeErr := f.events.ListEvents(admin, req); codeOf(scopeErr) != connect.CodePermissionDenied {
			t.Fatalf("widened scope: %v", scopeErr)
		}
	}
	req.WorkspaceId = f.workspaceID.String()
	scoped, err := f.events.ListEvents(admin, req)
	if err != nil || len(scoped.GetEvents()) != 1 {
		t.Fatalf("workspace search=%v %v", scoped, err)
	}
}

func TestAuditFiltersValidation(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"", uuid.NewString()} {
		req := &eventv1.ListEventsRequest{OrgId: uuid.NewString(), RequestId: id}
		if err := protovalidate.Validate(req); err != nil {
			t.Fatalf("rejected request ID %q: %v", id, err)
		}
	}
	for _, req := range []*eventv1.ListEventsRequest{
		{OrgId: uuid.NewString(), ActorIds: []string{"not-a-uuid"}},
		{OrgId: uuid.NewString(), ActorTypes: []string{"robot"}},
		{OrgId: uuid.NewString(), RequestId: "actor name"},
		{OrgId: uuid.NewString(), RequestId: uuid.NewString()[:12]},
		{OrgId: uuid.NewString(), RequestId: "%"},
	} {
		if err := protovalidate.Validate(req); err == nil {
			t.Fatalf("accepted %v", req)
		}
	}
}

func TestAuditRequestIDTimeBoundsAtEveryScope(t *testing.T) {
	t.Parallel()
	f := newScopedAuditFixture(t)
	principal, _ := reqctx.PrincipalFrom(f.owner)
	orgID := uuid.MustParse(f.orgID)
	requestID := uuid.NewString()
	ctx := reqctx.WithRequest(f.owner, reqctx.Request{ID: requestID})
	if err := events.Record(ctx, f.queries, events.Event{
		Type: events.TokenCreated, ResourceID: &f.resourceID,
	}); err != nil {
		t.Fatal(err)
	}
	scopes := []genDb.EntityType{
		genDb.EntityTypeOrganization, genDb.EntityTypeWorkspace, genDb.EntityTypeResource,
	}
	for _, scope := range scopes {
		req := &eventv1.ListEventsRequest{OrgId: orgID.String(), RequestId: requestID,
			ActorIds: []string{principal.Entity.ID.String()}, Types: []string{events.TokenCreated}}
		if scope != genDb.EntityTypeOrganization {
			req.WorkspaceId = f.workspaceID.String()
		}
		if scope == genDb.EntityTypeResource {
			req.ResourceId = f.resourceID.String()
		}
		res, err := f.events.ListEvents(f.owner, req)
		if err != nil || len(res.GetEvents()) != 1 {
			t.Fatalf("initial: %v %v", res, err)
		}
		at := res.GetEvents()[0].GetCreatedAt()
		req.StartTime = at
		included, err := f.events.ListEvents(f.owner, req)
		if err != nil || len(included.GetEvents()) != 1 {
			t.Fatalf("%s inclusive start: %v %v", scope, included, err)
		}
		req.StartTime = nil
		req.EndTime = at
		excluded, err := f.events.ListEvents(f.owner, req)
		if err != nil || len(excluded.GetEvents()) != 0 {
			t.Fatalf("%s exclusive end: %v %v", scope, excluded, err)
		}
	}
}
