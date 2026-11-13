package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

const (
	limitReason          = "large customer"
	workspaceContenders  = 4
	environmentsInSeeded = 1
)

// orgAdmin is the fixture's user acting with every organization scope.
func (f *deployFixture) orgAdmin(t *testing.T) context.Context {
	t.Helper()
	orgID := uuid.MustParse(f.organizationID(t))
	scopes := make([]genDb.EntityScope, 0, 4)
	for _, scope := range []genDb.Scope{genDb.ScopeMember, genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin} {
		scopes = append(
			scopes,
			genDb.EntityScope{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: scope},
		)
	}
	return f.asCreator(t, scopes...)
}

// asCreator acts as the user who created the fixture's organization, holding scopes.
func (f *deployFixture) asCreator(t *testing.T, scopes ...genDb.EntityScope) context.Context {
	t.Helper()
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: f.creatorID(t)}
	return reqctx.WithPrincipal(t.Context(), reqctx.Principal{Entity: entity, Scopes: scopes})
}

func (f *deployFixture) creatorID(t *testing.T) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT created_by FROM organizations WHERE id = $1`, f.organizationID(t)).
		Scan(&userID); err != nil {
		t.Fatalf("read organization creator: %v", err)
	}
	return userID
}

func (f *deployFixture) setLimit(t *testing.T, limit limits.Limit, value int32) *orgv1.OrgLimits {
	t.Helper()
	counts := &configv1.CountLimits{}
	limit.Set(counts, value)
	server := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	admin := f.asCreator(t, genDb.EntityScope{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin})
	resp, err := server.SetOrgLimits(admin, &orgv1.SetOrgLimitsRequest{
		OrgId:      f.organizationID(t),
		Limits:     counts,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{string(limit.Field)}},
		Reason:     limitReason,
	})
	if err != nil {
		t.Fatalf("set %s to %d: %v", limit.Field, value, err)
	}
	return resp.GetLimits()
}

func TestConcurrentWorkspaceCreatesStopAtTheLimit(t *testing.T) {
	f := newDeployFixture(t)
	f.setLimit(t, limits.WorkspacesPerOrg, 2)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	ctx := f.orgAdmin(t)

	errs := make([]error, workspaceContenders)
	var wg sync.WaitGroup
	for i := range workspaceContenders {
		wg.Go(func() {
			_, errs[i] = server.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{
				OrgId: f.organizationID(t),
				Name:  "contender",
			})
		})
	}
	wg.Wait()

	created := 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case codeOf(err) == connect.CodeResourceExhausted:
		default:
			t.Fatalf("create workspace: %v", err)
		}
	}
	if created != 1 {
		t.Fatalf("%d concurrent creates succeeded at the limit, want 1", created)
	}
	count, err := f.queries.CountOrgWorkspaces(t.Context(), uuid.MustParse(f.organizationID(t)))
	if err != nil || count != 2 {
		t.Fatalf("workspaces = %d (%v), want 2", count, err)
	}
}

func TestOrgLimitOverrideRaisesAndLowersACap(t *testing.T) {
	f := newDeployFixture(t)
	server := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	ctx := f.orgAdmin(t)
	create := func(name string) error {
		_, err := server.CreateEnvironment(ctx, &environmentv1.CreateEnvironmentRequest{
			WorkspaceId: f.workspaceID(t).String(),
			Name:        name,
			Type:        environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING,
		})
		return err
	}

	f.setLimit(t, limits.EnvironmentsPerWorkspace, environmentsInSeeded)
	err := create("staging")
	wantCode(t, err, connect.CodeResourceExhausted)
	if !strings.Contains(err.Error(), "1 of 1 environments") ||
		!strings.Contains(err.Error(), limits.EnvironmentsPerWorkspace.Env) {
		t.Fatalf("error = %v, want the count, the limit and its variable", err)
	}

	f.setLimit(t, limits.EnvironmentsPerWorkspace, environmentsInSeeded+1)
	if err = create("staging"); err != nil {
		t.Fatalf("create under the raised limit: %v", err)
	}

	f.setLimit(t, limits.EnvironmentsPerWorkspace, environmentsInSeeded)
	wantCode(t, create("preview"), connect.CodeResourceExhausted)
	count, err := f.queries.CountWorkspaceEnvironments(t.Context(), f.workspaceID(t))
	if err != nil || count != environmentsInSeeded+1 {
		t.Fatalf("environments = %d (%v): lowering the limit must delete nothing", count, err)
	}

	cleared := f.setLimit(t, limits.EnvironmentsPerWorkspace, 0)
	if len(cleared.GetOverridden()) != 0 {
		t.Fatalf("overridden = %v after clearing, want none", cleared.GetOverridden())
	}
	if err = create("preview"); err != nil {
		t.Fatalf("create under the install default: %v", err)
	}
}

func TestGetOrgLimitsReportsOverridesToMembers(t *testing.T) {
	f := newDeployFixture(t)
	server := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	req := &orgv1.GetOrgLimitsRequest{OrgId: f.organizationID(t)}

	before, err := server.GetOrgLimits(f.orgAdmin(t), req)
	if err != nil {
		t.Fatalf("get limits: %v", err)
	}
	if got := limits.ResourcesPerWorkspace.Get(
		before.GetLimits().GetLimits(),
	); got != limits.ResourcesPerWorkspace.Get(
		limits.Defaults(),
	) {
		t.Fatalf("resources per workspace = %d before an override, want the install default", got)
	}

	f.setLimit(t, limits.ResourcesPerWorkspace, 300)
	after, err := server.GetOrgLimits(f.orgAdmin(t), req)
	if err != nil {
		t.Fatalf("get limits: %v", err)
	}
	got := after.GetLimits()
	if limits.ResourcesPerWorkspace.Get(got.GetLimits()) != 300 {
		t.Fatalf("resources per workspace = %d, want the override", limits.ResourcesPerWorkspace.Get(got.GetLimits()))
	}
	if len(got.GetOverridden()) != 1 || got.GetOverridden()[0] != string(limits.ResourcesPerWorkspace.Field) {
		t.Fatalf("overridden = %v, want only %s", got.GetOverridden(), limits.ResourcesPerWorkspace.Field)
	}
	if got.GetReason() != limitReason || got.GetUpdatedAt() == nil {
		t.Fatalf("reason = %q, updated at %v, want the latest change", got.GetReason(), got.GetUpdatedAt())
	}

	outsider := secretAuthzContext(t)
	_, err = server.GetOrgLimits(outsider, req)
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestSetOrgLimitsRequiresSystemAdmin(t *testing.T) {
	f := newDeployFixture(t)
	server := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	req := &orgv1.SetOrgLimitsRequest{
		OrgId:      f.organizationID(t),
		Limits:     &configv1.CountLimits{WorkspacesPerOrg: 500},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{string(limits.WorkspacesPerOrg.Field)}},
		Reason:     limitReason,
	}
	_, err := server.SetOrgLimits(f.orgAdmin(t), req)
	wantCode(t, err, connect.CodePermissionDenied)
	if _, getErr := f.queries.GetOrgLimitOverrides(t.Context(), uuid.MustParse(f.organizationID(t))); getErr == nil {
		t.Fatal("a denied request stored an override")
	}
}

func TestSetOrgLimitsRefusesOrgsPerUser(t *testing.T) {
	f := newDeployFixture(t)
	server := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	_, err := server.SetOrgLimits(systemAdminContext(t), &orgv1.SetOrgLimitsRequest{
		OrgId:      f.organizationID(t),
		Limits:     &configv1.CountLimits{OrgsPerUser: 50},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{string(limits.OrgsPerUser.Field)}},
		Reason:     limitReason,
	})
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestSetOrgLimitsRequiresAReason(t *testing.T) {
	for name, reason := range map[string]string{"empty": "", "too long": strings.Repeat("x", 501)} {
		req := &orgv1.SetOrgLimitsRequest{
			OrgId:      uuid.NewString(),
			Limits:     &configv1.CountLimits{},
			UpdateMask: &fieldmaskpb.FieldMask{},
			Reason:     reason,
		}
		if err := protovalidate.Validate(req); err == nil {
			t.Errorf("%s reason accepted", name)
		}
	}
}

func TestSetOrgLimitsRecordsTheReasonAndValues(t *testing.T) {
	f := newDeployFixture(t)
	f.setLimit(t, limits.WebhooksPerWorkspace, 40)

	recorded := f.orgEvents(t, events.OrgLimitsChanged)
	if len(recorded) != 1 {
		t.Fatalf("%d %s events, want 1", len(recorded), events.OrgLimitsChanged)
	}
	var data struct {
		Reason string           `json:"reason"`
		Old    map[string]int32 `json:"old"`
		New    map[string]int32 `json:"new"`
	}
	if err := json.Unmarshal(recorded[0].Data, &data); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	field := string(limits.WebhooksPerWorkspace.Field)
	if data.Reason != limitReason ||
		data.Old[field] != limits.WebhooksPerWorkspace.Get(limits.Defaults()) ||
		data.New[field] != 40 {
		t.Fatalf("event data = %+v, want the reason and the old and new %s", data, field)
	}
}

func TestApplyPastTheResourceLimitChangesNothing(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.setLimit(t, limits.ResourcesPerWorkspace, 1)
	revision := f.revision(t, f.envID)

	_, err := applyFile(t, f, planFileHeader+applyFileWorker, applyOptions{revision: revision}, f.applyScopes(t))
	wantCode(t, err, connect.CodeResourceExhausted)
	if f.hasResource(t, applyWorker) || f.revision(t, f.envID) != revision {
		t.Fatal("a refused apply created the service")
	}
}

func TestApplyPastTheDomainLimitChangesNothing(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.setLimit(t, limits.DomainsPerResource, 1)
	file := planFileHeader + strings.Replace(
		applyFileWorker,
		"[worker.loco.test]",
		"[worker.loco.test, api.loco.test]",
		1,
	)

	_, err := applyFile(t, f, file, applyOptions{revision: f.revision(t, f.envID)}, f.applyScopes(t))
	wantCode(t, err, connect.CodeResourceExhausted)
	if f.hasResource(t, applyWorker) {
		t.Fatal("a refused apply created the service")
	}
}

func TestCreateOrgStopsAtTheCreatorsLimit(t *testing.T) {
	f := newDeployFixture(t)
	counts := limits.Defaults()
	limits.OrgsPerUser.Set(counts, 1)
	server := NewOrgServer(f.pool, f.queries, InvitationConfig{}, counts)
	creator := f.asCreator(t, genDb.EntityScope{
		EntityType: genDb.EntityTypeUser, EntityID: f.creatorID(t), Scope: genDb.ScopeWrite,
	})
	_, err := server.CreateOrg(creator, &orgv1.CreateOrgRequest{Name: new("second")})
	wantCode(t, err, connect.CodeResourceExhausted)
}
