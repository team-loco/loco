package service

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	platformAllocationGate    = 913477
	platformDeactivate        = "deactivate"
	platformRename            = "rename"
	platformResourceAllocator = "resource"
	platformPlanAllocator     = "plan"
)

func platformAllocator(t *testing.T, f *deployFixture, kind string, id uuid.UUID) func() error {
	t.Helper()
	scoped := context.WithValue(t.Context(), contextkeys.EntityScopesKey, f.applyScopes(t))
	input := &domainv1.DomainInput{
		DomainSource:     domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED,
		PlatformDomainId: new(id.String()),
		Subdomain:        new("allocated"),
	}
	switch kind {
	case platformResourceAllocator:
		server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
		req := &resourcev1.CreateResourceRequest{
			WorkspaceId: f.workspaceID(t).String(),
			Name:        "allocated",
			Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
			Spec: &resourcev1.ResourceSpec{
				Spec: &resourcev1.ResourceSpec_Service{
					Service: &resourcev1.ServiceSpec{Routing: &resourcev1.RoutingConfig{Port: 8080}},
				},
			},
			Domain: input,
		}
		return func() error { _, err := server.CreateResource(scoped, req); return err }
	case platformDomainNameField:
		c := newDomainClient(t, f)
		return func() error {
			_, err := c.server.CreateResourceDomain(
				scoped,
				&domainv1.CreateResourceDomainRequest{ResourceId: f.resourceID.String(), Domain: input},
			)
			return err
		}
	case platformPlanAllocator:
		if _, err := f.pool.Exec(
			t.Context(),
			"UPDATE clusters SET health_status='healthy',last_heartbeat=now()",
		); err != nil {
			t.Fatal(err)
		}
		file := planFileHeader + strings.ReplaceAll(applyFileWorker, testPlatformDomain, platformOriginalName)
		server := newPlanServer(f)
		planned, err := server.Plan(scoped, &planv1.PlanRequest{File: []byte(file), EnvironmentId: f.envID.String()})
		if err != nil {
			t.Fatal(err)
		}
		if len(planned.GetErrors()) > 0 {
			t.Fatalf("plan fixture errors: %v", planned.GetErrors())
		}
		req := &planv1.ApplyRequest{
			File:          []byte(file),
			EnvironmentId: f.envID.String(),
			Revision:      planned.GetRevision(),
			Images:        planned.GetImages(),
		}
		return func() error { _, err := server.Apply(scoped, req); return err }
	default:
		t.Fatalf("unknown allocator %q", kind)
		return nil
	}
}

func TestPlatformAllocationReadsTheRootAfterConcurrentUpdate(t *testing.T) {
	for _, kind := range []string{platformResourceAllocator, platformDomainNameField, platformPlanAllocator} {
		for _, change := range []string{platformRename, platformDeactivate} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				f := newDeployFixture(t)
				_, ctx, id := platformOperator(t, f)
				allocate := platformAllocator(t, f, kind, id)
				holder, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback(context.Background())
				if _, err := holder.Exec(
					ctx,
					"SELECT id FROM platform_domains WHERE id=$1 FOR UPDATE",
					id,
				); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- allocate() }()
				f.waitForLockWaiters(t, 1)
				domain, active := platformOriginalName, true
				if change == platformRename {
					domain = platformUpdatedName
				} else {
					active = false
				}
				if _, err := holder.Exec(
					ctx,
					"UPDATE platform_domains SET domain=$2,is_active=$3 WHERE id=$1",
					id,
					domain,
					active,
				); err != nil {
					t.Fatal(err)
				}
				if err := holder.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				result := <-done
				if change == platformDeactivate || kind == platformPlanAllocator {
					if connect.CodeOf(result) != connect.CodeFailedPrecondition {
						t.Fatalf("allocation error=%v, want failed_precondition", result)
					}
					var count int
					if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM resource_domains").Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count != 0 {
						t.Fatalf("refused allocation left %d domains", count)
					}
					return
				}
				if result != nil {
					t.Fatal(result)
				}
				var allocated string
				if err := f.pool.QueryRow(ctx, "SELECT domain FROM resource_domains WHERE platform_domain_id=$1", id).
					Scan(&allocated); err != nil {
					t.Fatal(err)
				}
				if allocated != "allocated."+platformUpdatedName {
					t.Fatalf("allocated %q with stale root", allocated)
				}
			})
		}
	}
}

func TestPlatformUpdateWaitsForEveryDomainAllocator(t *testing.T) {
	for _, kind := range []string{platformResourceAllocator, platformDomainNameField, platformPlanAllocator} {
		for _, change := range []string{platformRename, platformDeactivate} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				f := newDeployFixture(t)
				server, ctx, id := platformOperator(t, f)
				allocate := platformAllocator(t, f, kind, id)
				const gateTrigger = `CREATE FUNCTION hold_platform_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_advisory_xact_lock(913477); RETURN NEW; END $$;
CREATE TRIGGER hold_platform_allocation BEFORE INSERT ON resource_domains
FOR EACH ROW EXECUTE FUNCTION hold_platform_allocation();`
				if _, err := f.pool.Exec(ctx, gateTrigger); err != nil {
					t.Fatal(err)
				}
				gate, gateErr := f.pool.Begin(ctx)
				if gateErr != nil {
					t.Fatal(gateErr)
				}
				defer gate.Rollback(context.Background())
				if _, err := gate.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", platformAllocationGate); err != nil {
					t.Fatal(err)
				}
				allocated := make(chan error, 1)
				go func() { allocated <- allocate() }()
				f.waitForLockWaiters(t, 1)
				updated := make(chan error, 1)
				request := &domainv1.UpdatePlatformDomainRequest{Id: id.String()}
				if change == platformRename {
					request.Domain = new(platformUpdatedName)
				} else {
					request.IsActive = new(false)
				}
				go func() {
					_, err := server.UpdatePlatformDomain(ctx, request)
					updated <- err
				}()
				f.waitForLockWaiters(t, 2)
				if err := gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-allocated; err != nil {
					t.Fatal(err)
				}
				result := <-updated
				if change == platformRename && connect.CodeOf(result) != connect.CodeFailedPrecondition {
					t.Fatalf("rename after allocation=%v, want failed_precondition", result)
				}
				if change == platformDeactivate && result != nil {
					t.Fatalf("deactivate after allocation: %v", result)
				}
				root, err := f.queries.GetPlatformDomain(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if root.Domain != platformOriginalName || root.IsActive != (change == platformRename) {
					t.Fatalf("refused rename changed root: %+v", root)
				}
				var mismatch int
				if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM resource_domains
WHERE platform_domain_id=$1 AND domain NOT LIKE '%.' || $2`, id, root.Domain).
					Scan(&mismatch); err != nil {
					t.Fatal(err)
				}
				if mismatch != 0 {
					t.Fatalf("%d assigned hosts no longer match the root", mismatch)
				}
			})
		}
	}
}
