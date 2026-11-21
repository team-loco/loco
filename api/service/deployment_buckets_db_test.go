package service

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

var bucketsBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type bucketsFixture struct {
	*deployFixture
	workspaceID uuid.UUID
	web, worker uuid.UUID
	hidden      uuid.UUID
}

func newBucketsFixture(t *testing.T) bucketsFixture {
	t.Helper()
	f := newDeployFixture(t)
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	return bucketsFixture{
		deployFixture: f,
		workspaceID:   resource.WorkspaceID,
		web:           f.tenant.resource(t, resource.WorkspaceID, "web", &resourcev1.ServiceSpec{}),
		worker:        f.tenant.resource(t, resource.WorkspaceID, "worker", &resourcev1.ServiceSpec{}),
		hidden:        f.tenant.resource(t, resource.WorkspaceID, "hidden", &resourcev1.ServiceSpec{}),
	}
}

// seed records a deployment of resourceID in the fixture environment created at createdAt.
func (f bucketsFixture) seed(
	t *testing.T,
	resourceID uuid.UUID,
	status genDb.DeploymentStatus,
	createdAt time.Time,
	image string,
) uuid.UUID {
	t.Helper()
	params := f.paramsFor(f.clusterID)
	params.ResourceID = resourceID
	id, err := f.queries.CreateDeployment(t.Context(), params.CreateDeploymentParams)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(t.Context(), `
UPDATE deployments SET created_at = $2, status = $3, spec = jsonb_set(spec, '{build}', jsonb_build_object('image', $4::text))
WHERE id = $1`, id, createdAt, status, image)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f bucketsFixture) readers() []genDb.EntityScope {
	return []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeResource, EntityID: f.web, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeResource, EntityID: f.worker, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeResource, EntityID: f.hidden, Scope: genDb.ScopeWrite},
	}
}

func (f bucketsFixture) query(
	t *testing.T,
	req *deploymentv1.QueryDeploymentBucketsRequest,
	scopes []genDb.EntityScope,
) []*deploymentv1.DeploymentBucket {
	t.Helper()
	req.WorkspaceId = f.workspaceID.String()
	req.EnvironmentId = f.envID.String()
	if err := protovalidate.Validate(req); err != nil {
		t.Fatalf("invalid request: %v", err)
	}
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: scopes})
	resp, err := f.deploymentServer().QueryDeploymentBuckets(ctx, req)
	if err != nil {
		t.Fatalf("query deployment buckets: %v", err)
	}
	return resp.GetBuckets()
}

func statusCounts(bucket *deploymentv1.DeploymentBucket) map[deploymentv1.DeploymentStatus]int32 {
	counts := map[deploymentv1.DeploymentStatus]int32{}
	for _, c := range bucket.GetStatusCounts() {
		counts[c.GetStatus()] = c.GetCount()
	}
	return counts
}

func TestQueryDeploymentBucketsSummarizesEachBucket(t *testing.T) {
	t.Parallel()
	f := newBucketsFixture(t)
	until := bucketsBase.Add(12 * time.Hour)
	only := f.seed(t, f.resourceID, genDb.DeploymentStatusFailed, bucketsBase, "nginx:1.0")
	f.seed(t, f.resourceID, genDb.DeploymentStatusRunning, bucketsBase.Add(time.Hour), "nginx:1.1")
	f.seed(t, f.web, genDb.DeploymentStatusFailed, bucketsBase.Add(90*time.Minute), "web:2")
	f.seed(t, f.worker, genDb.DeploymentStatusDegraded, bucketsBase.Add(2*time.Hour-time.Microsecond), "worker:3")
	f.seed(t, f.hidden, genDb.DeploymentStatusRunning, bucketsBase.Add(80*time.Minute), "hidden:1")
	f.seed(t, f.resourceID, genDb.DeploymentStatusRunning, until.Add(-time.Microsecond), "nginx:1.2")
	f.seed(t, f.resourceID, genDb.DeploymentStatusRunning, until, "nginx:late")
	f.seed(t, f.resourceID, genDb.DeploymentStatusRunning, bucketsBase.Add(-time.Microsecond), "nginx:early")
	staging := f.paramsFor(f.clusterID)
	staging.EnvironmentID = f.addStagingEnvironment(t)
	if _, err := f.queries.CreateDeployment(t.Context(), staging.CreateDeploymentParams); err != nil {
		t.Fatal(err)
	}

	buckets := f.query(t, &deploymentv1.QueryDeploymentBucketsRequest{
		Since:            timestamppb.New(bucketsBase),
		Until:            timestamppb.New(until),
		BucketCount:      12,
		MaxResourceNames: 2,
	}, f.readers())

	var indexes []int32
	for _, b := range buckets {
		indexes = append(indexes, b.GetIndex())
	}
	if !slices.Equal(indexes, []int32{0, 1, 11}) {
		t.Fatalf("bucket indexes = %v, want [0 1 11]: since is inside, until outside", indexes)
	}
	first, busy, last := buckets[0], buckets[1], buckets[2]
	want := &deploymentv1.DeploymentBucketEntry{
		Id:           only.String(),
		ResourceId:   f.resourceID.String(),
		ResourceName: "svc",
		Status:       deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED,
		CreatedAt:    timestamppb.New(bucketsBase),
		Image:        "nginx:1.0",
	}
	if got := first.GetOnlyDeployment(); got.String() != want.String() {
		t.Errorf("only deployment = %v, want %v", got, want)
	}
	if first.GetDeploymentCount() != 1 || last.GetDeploymentCount() != 1 || last.GetOnlyDeployment().GetImage() != "nginx:1.2" {
		t.Errorf("edge buckets = %v and %v, want one deployment each", first, last)
	}

	if busy.GetDeploymentCount() != 3 || busy.GetOnlyDeployment() != nil {
		t.Fatalf("bucket 1 = %v, want three deployments without the hidden resource's", busy)
	}
	wantCounts := map[deploymentv1.DeploymentStatus]int32{
		deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING:  1,
		deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED:   1,
		deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEGRADED: 1,
	}
	if got := statusCounts(busy); !maps.Equal(got, wantCounts) {
		t.Errorf("bucket 1 status counts = %v, want %v", got, wantCounts)
	}
	if busy.GetResourceCount() != 3 || !slices.Equal(busy.GetResourceNames(), []string{"worker", "web"}) {
		t.Errorf("bucket 1 resources = %d %v, want 3 with the two newest names", busy.GetResourceCount(), busy.GetResourceNames())
	}
	mean := bucketsBase.Add(time.Hour + (30*time.Minute+time.Hour-time.Microsecond)/3)
	if got := busy.GetMeanCreatedAt().AsTime(); got.Sub(mean).Abs() > time.Millisecond {
		t.Errorf("bucket 1 mean = %v, want %v", got, mean)
	}

	workspace := []genDb.EntityScope{{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeRead}}
	buckets = f.query(t, &deploymentv1.QueryDeploymentBucketsRequest{
		Since: timestamppb.New(bucketsBase), Until: timestamppb.New(until), BucketCount: 12,
	}, workspace)
	if buckets[1].GetDeploymentCount() != 4 || len(buckets[1].GetResourceNames()) != 0 {
		t.Errorf("workspace reader's bucket 1 = %v, want four deployments and no names", buckets[1])
	}
}

func TestQueryDeploymentBucketsGroupsByResource(t *testing.T) {
	t.Parallel()
	f := newBucketsFixture(t)
	f.seed(t, f.resourceID, genDb.DeploymentStatusRunning, bucketsBase.Add(time.Minute), "nginx:1")
	f.seed(t, f.resourceID, genDb.DeploymentStatusFailed, bucketsBase.Add(2*time.Minute), "nginx:2")
	f.seed(t, f.web, genDb.DeploymentStatusRunning, bucketsBase.Add(40*time.Minute), "web:1")
	f.seed(t, f.worker, genDb.DeploymentStatusRunning, bucketsBase.Add(time.Minute), "worker:1")
	f.seed(t, f.hidden, genDb.DeploymentStatusRunning, bucketsBase.Add(time.Minute), "hidden:1")

	buckets := f.query(t, &deploymentv1.QueryDeploymentBucketsRequest{
		ResourceIds:      []string{f.resourceID.String(), f.web.String(), f.hidden.String()},
		Since:            timestamppb.New(bucketsBase),
		Until:            timestamppb.New(bucketsBase.Add(time.Hour)),
		BucketCount:      4,
		GroupByResource:  true,
		MaxResourceNames: 3,
	}, f.readers())

	type key struct {
		resource string
		index    int32
		count    int32
	}
	var got []key
	for _, b := range buckets {
		got = append(got, key{b.GetResourceId(), b.GetIndex(), b.GetDeploymentCount()})
	}
	want := []key{{f.resourceID.String(), 0, 2}, {f.web.String(), 2, 1}}
	slices.SortFunc(want, func(a, b key) int { return strings.Compare(a.resource, b.resource) })
	if !slices.Equal(got, want) {
		t.Fatalf("buckets = %v, want %v: the filter leaves out worker and the caller cannot read hidden", got, want)
	}
	for _, b := range buckets {
		if b.GetResourceId() == f.resourceID.String() && !slices.Equal(b.GetResourceNames(), []string{"svc"}) {
			t.Errorf("svc bucket names = %v", b.GetResourceNames())
		}
	}
}

func TestQueryDeploymentBucketsValidation(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	valid := func() *deploymentv1.QueryDeploymentBucketsRequest {
		return &deploymentv1.QueryDeploymentBucketsRequest{
			WorkspaceId: id, EnvironmentId: id, BucketCount: 12,
			Since: timestamppb.New(bucketsBase), Until: timestamppb.New(bucketsBase.Add(time.Hour)),
		}
	}
	if err := protovalidate.Validate(valid()); err != nil {
		t.Fatal(err)
	}
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	for name, change := range map[string]func(*deploymentv1.QueryDeploymentBucketsRequest){
		"no buckets":       func(r *deploymentv1.QueryDeploymentBucketsRequest) { r.BucketCount = 0 },
		"too many buckets": func(r *deploymentv1.QueryDeploymentBucketsRequest) { r.BucketCount = 201 },
		"no since":         func(r *deploymentv1.QueryDeploymentBucketsRequest) { r.Since = nil },
		"no until":         func(r *deploymentv1.QueryDeploymentBucketsRequest) { r.Until = nil },
		"empty window":     func(r *deploymentv1.QueryDeploymentBucketsRequest) { r.Until = r.GetSince() },
		"many resources":   func(r *deploymentv1.QueryDeploymentBucketsRequest) { r.ResourceIds = tooMany },
		"too many names":   func(r *deploymentv1.QueryDeploymentBucketsRequest) { r.MaxResourceNames = 21 },
		"no environment":   func(r *deploymentv1.QueryDeploymentBucketsRequest) { r.EnvironmentId = "" },
	} {
		req := valid()
		change(req)
		if err := protovalidate.Validate(req); err == nil {
			t.Errorf("%s: accepted %v", name, req)
		}
	}
}

func TestListDeploymentsFiltersByStatus(t *testing.T) {
	t.Parallel()
	f := newBucketsFixture(t)
	failed := f.seed(t, f.web, genDb.DeploymentStatusFailed, bucketsBase.Add(time.Minute), "web:1")
	crashed := f.seed(t, f.resourceID, genDb.DeploymentStatusCrashed, bucketsBase.Add(2*time.Minute), "nginx:1")
	f.seed(t, f.resourceID, genDb.DeploymentStatusRunning, bucketsBase.Add(3*time.Minute), "nginx:2")
	f.seed(t, f.hidden, genDb.DeploymentStatusFailed, bucketsBase.Add(4*time.Minute), "hidden:1")

	page := f.listDeployments(t, &deploymentv1.ListDeploymentsRequest{
		WorkspaceId:   f.workspaceID.String(),
		EnvironmentId: f.envID.String(),
		Statuses: []deploymentv1.DeploymentStatus{
			deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED,
			deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CRASHED,
		},
		IncludeTotalCount: true,
	}, f.readers()...)
	if got := deploymentIDs(page.GetDeployments()); !slices.Equal(got, []string{crashed.String(), failed.String()}) {
		t.Fatalf("failed deployments = %v, want %s and %s", got, crashed, failed)
	}
	if page.GetTotalCount() != 2 {
		t.Errorf("total = %d, want 2", page.GetTotalCount())
	}
	if names := []string{page.GetDeployments()[0].GetResourceName(), page.GetDeployments()[1].GetResourceName()}; !slices.Equal(names, []string{"svc", "web"}) {
		t.Errorf("resource names = %v, want [svc web]", names)
	}

	page = f.listDeployments(t, &deploymentv1.ListDeploymentsRequest{
		ResourceId: f.resourceID.String(),
		Statuses:   []deploymentv1.DeploymentStatus{deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING},
	}, f.readers()...)
	if len(page.GetDeployments()) != 1 || page.GetDeployments()[0].GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING {
		t.Errorf("running deployments of svc = %v", deploymentIDs(page.GetDeployments()))
	}

	for _, statuses := range [][]deploymentv1.DeploymentStatus{
		{deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_UNSPECIFIED},
		{deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED},
	} {
		req := &deploymentv1.ListDeploymentsRequest{ResourceId: f.resourceID.String(), Statuses: statuses}
		if err := protovalidate.Validate(req); err == nil {
			t.Errorf("accepted statuses %v", statuses)
		}
	}
}
