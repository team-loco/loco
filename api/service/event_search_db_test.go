package service

import (
	"slices"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func TestListEventsSearchAndActors(t *testing.T) {
	f := newScopedAuditFixture(t)
	principal, _ := reqctx.PrincipalFrom(f.owner)
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE users SET name=$1 WHERE id=$2",
		"Ada Audit",
		principal.Entity.ID,
	); err != nil {
		t.Fatal(err)
	}
	orgID := uuid.MustParse(f.orgID)
	tokenID := uuid.New()
	var primary []int64
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
				RequestID: new(
					"search-request-" + string(rune('a'+i)),
				),
				Data: []byte(`{"name":"Checkout API","domain":"api.example.test"}`),
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
		name  string
		ids   []string
		types []string
		query string
		want  []int64
	}{
		{name: "person", ids: []string{principal.Entity.ID.String()}, want: []int64{primary[4], primary[0]}},
		{name: "token", ids: []string{tokenID.String()}, want: []int64{primary[1]}},
		{name: "Loco", types: []string{events.ActorSystem}, want: []int64{primary[2]}},
		{name: "cluster agent", types: []string{"agent"}, want: []int64{primary[3]}},
		{name: "person and Loco", ids: []string{principal.Entity.ID.String()},
			types: []string{events.ActorSystem}, want: []int64{primary[4], primary[2], primary[0]}},
		{name: "request prefix", query: "search-request-",
			want: []int64{primary[4], primary[3], primary[2], primary[1], primary[0]}},
		{name: "request exact", query: "search-request-b", want: []int64{primary[1]}},
		{name: "actor name", query: "DA AU", want: []int64{primary[4], primary[0]}},
		{name: "actor email", query: "NER@ACME", want: []int64{primary[4], primary[0]}},
		{name: "target", query: "CHECKOUT", want: []int64{primary[4], primary[3], primary[2], primary[1], primary[0]}},
		{name: "domain", query: "EXAMPLE", want: []int64{primary[4], primary[3], primary[2], primary[1], primary[0]}},
		{name: "overlapping actor and target", query: "test",
			want: []int64{primary[4], primary[3], primary[2], primary[1], primary[0]}},
		{name: "combined", ids: []string{tokenID.String()}, query: "search-request-b", want: []int64{primary[1]}},
		{name: "literal wildcard", query: "%%%"},
		{name: "request suffix is not prefix", query: "request-b"},
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
					Query:       tc.query,
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
		OrgId:    f.orgID,
		Types:    []string{events.TokenCreated},
		Query:    "Ada",
		ActorIds: []string{principal.Entity.ID.String()},
	}
	orgResult, err := f.events.ListEvents(f.owner, req)
	if err != nil || len(orgResult.GetEvents()) != 4 {
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
	if err != nil || len(scoped.GetEvents()) != 2 {
		t.Fatalf("workspace search=%v %v", scoped, err)
	}
}

func TestAuditSearchValidation(t *testing.T) {
	for _, query := range []string{"", "abc"} {
		req := &eventv1.ListEventsRequest{OrgId: uuid.NewString(), Query: query}
		if err := protovalidate.Validate(req); err != nil {
			t.Fatalf("rejected query %q: %v", query, err)
		}
	}
	for _, req := range []*eventv1.ListEventsRequest{
		{OrgId: uuid.NewString(), ActorIds: []string{"not-a-uuid"}},
		{OrgId: uuid.NewString(), ActorTypes: []string{"not-an-actor-type"}},
		{OrgId: uuid.NewString(), Query: strings.Repeat("x", 257)},
		{OrgId: uuid.NewString(), Query: "a"},
		{OrgId: uuid.NewString(), Query: "ab"},
	} {
		if err := protovalidate.Validate(req); err == nil {
			t.Fatalf("accepted %v", req)
		}
	}
}
