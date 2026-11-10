package service

import (
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

func TestClusterRegistrationResumesWithoutReplacingEnrolledIdentity(t *testing.T) {
	e := newEnrollmentFixture(t)
	ctx := systemAdminContext(t)
	request := &clusterv1.CreateClusterRequest{RequestId: uuid.NewString(), Name: "installed", Region: testRegion,
		Tier: environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING}
	first, err := e.admin.CreateCluster(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := e.admin.CreateCluster(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if retry.GetClusterId() != first.GetClusterId() ||
		retry.GetJoinToken().GetToken() == first.GetJoinToken().GetToken() {
		t.Fatal("retry must reuse registration and replace only pending enrollment token")
	}
	key := newAgentKey(t)
	clusterKey := uuid.New()
	if _, enrollErr := e.enroll(t, retry.GetJoinToken().GetToken(), clusterKey, key); enrollErr != nil {
		t.Fatal(enrollErr)
	}
	resumed, err := e.admin.CreateCluster(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.GetClusterId() != first.GetClusterId() || resumed.GetJoinToken() != nil {
		t.Fatal("enrolled registration must not issue replacement token")
	}
	if got := e.fingerprint(t, uuid.MustParse(first.GetClusterId())); got != keyFingerprint(t, key) {
		t.Fatal("retry replaced enrolled identity")
	}
	request.Region = "changed"
	_, err = e.admin.CreateCluster(ctx, request)
	requireCode(t, err, connect.CodeFailedPrecondition, "changed registration")
}

func TestConcurrentClusterRegistrationCreatesOneRecord(t *testing.T) {
	e := newEnrollmentFixture(t)
	request := &clusterv1.CreateClusterRequest{RequestId: uuid.NewString(), Name: "installed", Region: testRegion,
		Tier: environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING}
	const contenders = 8
	responses := make([]*clusterv1.CreateClusterResponse, contenders)
	errs := make([]error, contenders)
	ctx := systemAdminContext(t)
	var workers sync.WaitGroup
	for i := range contenders {
		workers.Go(func() { responses[i], errs[i] = e.admin.CreateCluster(ctx, request) })
	}
	workers.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
		if responses[i].GetClusterId() != responses[0].GetClusterId() {
			t.Fatal("concurrent retry duplicated cluster")
		}
	}
	var count int
	row := e.pool.QueryRow(
		t.Context(), "SELECT count(*) FROM clusters WHERE id = $1", request.GetRequestId(),
	)
	if scanErr := row.Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	if count != 1 {
		t.Fatalf("registration created %d clusters", count)
	}
	id := uuid.MustParse(responses[0].GetClusterId())
	if _, err := e.pool.Exec(t.Context(), "UPDATE clusters SET builds_enabled = true WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	listed, err := e.admin.ListClusters(ctx, &clusterv1.ListClustersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, cluster := range listed.GetClusters() {
		if cluster.GetId() == id.String() && !cluster.GetBuildsEnabled() {
			t.Fatal("admin status lost build capability")
		}
	}
	if _, pauseErr := manageCluster(ctx, e.pool, clusterPause, id, false); pauseErr != nil {
		t.Fatal(pauseErr)
	}
	if _, removeErr := manageCluster(ctx, e.pool, clusterRemove, id, false); removeErr != nil {
		t.Fatal(removeErr)
	}
	_, err = e.admin.CreateCluster(ctx, request)
	requireCode(t, err, connect.CodeFailedPrecondition, "removed registration")
}
