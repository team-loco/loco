package service

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/publicrouting"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	routingv1 "github.com/team-loco/loco/gen/go/loco/routing/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const routingTestDrainSeconds int32 = 120

type routingFixture struct {
	*deployFixture
	server     *RoutingServer
	reconciler *publicrouting.Reconciler
	publisher  uuid.UUID
	session    string
	ctx        context.Context
	domain     *domainClient
}

func newRoutingFixture(t *testing.T) *routingFixture {
	t.Helper()
	f := newDeployFixture(t)
	server := NewRoutingServer(f.pool, f.queries, 60, 2, routingTestDrainSeconds)
	bound, err := server.BindPublisher(
		systemAdminContext(t),
		&routingv1.BindPublisherRequest{AppDomain: testAppDomain, ReservedPrefixes: []string{"loco-net-"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(bound.GetPublisherId())
	scoped := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			Scopes: []db.EntityScope{
				{EntityType: db.EntityTypeRoutingPublisher, EntityID: id, Scope: db.ScopeRead},
				{EntityType: db.EntityTypeRoutingPublisher, EntityID: id, Scope: db.ScopeWrite},
			},
		},
	)
	return &routingFixture{
		deployFixture: f,
		server:        server,
		reconciler: publicrouting.New(
			f.pool,
			publicrouting.Config{Interval: time.Second, RetrySeconds: 1, BatchSize: 100},
		),
		publisher: id,
		session:   uuid.NewString(),
		ctx:       scoped,
		domain:    newDomainClient(t, f),
	}
}

func (f *routingFixture) reconcile(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), "UPDATE public_routing_dirty SET next_attempt_at=NOW()"); err != nil {
		t.Fatal(err)
	}
	if err := f.reconciler.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func (f *routingFixture) publications(t *testing.T) []db.PublicRoutePublication {
	t.Helper()
	rows, err := f.queries.ListResourceRoutePublications(
		t.Context(),
		db.ListResourceRoutePublicationsParams{ResourceID: f.resourceID, EnvironmentID: f.envID},
	)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func (f *routingFixture) list(t *testing.T, after int64) *routingv1.ListRouteIntentsResponse {
	t.Helper()
	response, err := f.server.ListRouteIntents(
		f.ctx,
		&routingv1.ListRouteIntentsRequest{
			PublisherId:   f.publisher.String(),
			SessionId:     f.session,
			AfterSequence: after,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func (f *routingFixture) ack(t *testing.T, p db.PublicRoutePublication) {
	t.Helper()
	f.list(t, 0)
	_, err := f.server.ReportPublication(
		f.ctx,
		&routingv1.ReportPublicationRequest{
			PublisherId:   f.publisher.String(),
			SessionId:     f.session,
			PublicationId: p.ID.String(),
			Revision:      p.Revision,
			Outcome:       routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}
func (f *routingFixture) ready(t *testing.T, cluster uuid.UUID, ready bool) {
	t.Helper()
	p := f.placement(t, cluster)
	var payload loco.PlacementPayload
	if err := json.Unmarshal(p.DesiredSpec, &payload); err != nil {
		t.Fatal(err)
	}
	route := loco.PublicRouteStatus{
		Revision:  p.DesiredRevision,
		Hostnames: payload.AppSpec.ServiceSpec.Routing.Hostnames,
		Ready:     ready,
	}
	encoded, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE placements SET ready_replicas=1,public_route=$2 WHERE id=$1",
		p.ID,
		encoded,
	); err != nil {
		t.Fatal(err)
	}
}
func (f *routingFixture) deploy(t *testing.T) {
	t.Helper()
	if err := f.domain.add(testPrimaryDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := createTestDeployment(t, f.deployFixture, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
}

func TestPublicRoutingAdmissionAndRetirement(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.reconcile(t)
	pending := f.publications(t)[0]
	if len(pending.ClusterIds) != 0 {
		t.Fatalf("unaccepted route advertised: %+v", pending)
	}
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	admitted := f.publications(t)[0]
	if !slices.Equal(admitted.ClusterIds, []uuid.UUID{f.clusterID}) {
		t.Fatalf("admitted clusters = %v", admitted.ClusterIds)
	}
	f.ack(t, admitted)
	// A temporary failure retains the admitted route rather than inventing health failover.
	f.ready(t, f.clusterID, false)
	f.reconcile(t)
	if got := f.publications(t)[0]; got.Revision != admitted.Revision ||
		!slices.Equal(got.ClusterIds, admitted.ClusterIds) {
		t.Fatalf("transient failure changed publication: %+v", got)
	}
	err := withTx(
		t.Context(),
		f.pool,
		func(q *db.Queries) error { return removePlacement(t.Context(), q, f.resourceID, f.envID, f.clusterID) },
	)
	if err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	withdrawing := f.publications(t)[0]
	if !withdrawing.Withdrawing || len(withdrawing.ClusterIds) != 0 {
		t.Fatalf("retirement intent = %+v", withdrawing)
	}
	if f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("placement deleted before acknowledgement")
	}
	// Restarting the reconciler cannot forget the pending retirement.
	f.reconciler = publicrouting.New(
		f.pool,
		publicrouting.Config{Interval: time.Second, RetrySeconds: 1, BatchSize: 100},
	)
	f.ack(t, withdrawing)
	f.reconciler = publicrouting.New(
		f.pool,
		publicrouting.Config{Interval: time.Second, RetrySeconds: 1, BatchSize: 100},
	)
	f.reconcile(t)
	if f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("placement deleted before drain")
	}
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second'",
	); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if !f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("acknowledged and drained placement remains")
	}
	t.Log(
		"Admission and durable retirement checks passed",
	)
}

func TestPublicRoutingRenameRetainsOldLocalHostname(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	f.ack(t, f.publications(t)[0])
	_, err := f.domain.server.UpdateResourceDomain(
		f.domain.ctx,
		&domainv1.UpdateResourceDomainRequest{
			DomainId: f.domain.domainID(t, testPrimaryDomain),
			Domain:   new(testSecondDomain),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	names := desiredPayload(t, f.deployFixture).AppSpec.ServiceSpec.Routing.Hostnames
	if !slices.Contains(names, testPrimaryDomain) || !slices.Contains(names, testSecondDomain) {
		t.Fatalf("rename did not retain names: %v", names)
	}
	f.reconcile(t)
	var withdrawal db.PublicRoutePublication
	for _, p := range f.publications(t) {
		if p.Hostname == testPrimaryDomain {
			withdrawal = p
		}
	}
	if !withdrawal.Withdrawing {
		t.Fatal("old hostname has no withdrawal tombstone")
	}
	f.ack(t, withdrawal)
	f.reconcile(t)
	if !slices.Contains(desiredPayload(t, f.deployFixture).AppSpec.ServiceSpec.Routing.Hostnames, testPrimaryDomain) {
		t.Fatal("old route removed before drain")
	}
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second' WHERE id=$1",
		withdrawal.ID,
	); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	names = desiredPayload(t, f.deployFixture).AppSpec.ServiceSpec.Routing.Hostnames
	if !slices.Equal(names, []string{testSecondDomain}) {
		t.Fatalf("drained local hostnames = %v", names)
	}
}

func TestRoutingPublicationAuthorizationSessionsAndRevisions(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	p := f.publications(t)[0]
	req := &routingv1.ListRouteIntentsRequest{PublisherId: f.publisher.String(), SessionId: f.session}
	_, err := f.server.ListRouteIntents(f.domain.ctx, req)
	wantCode(t, err, connect.CodePermissionDenied)
	another := uuid.NewString()
	req.PublisherId = another
	_, err = f.server.ListRouteIntents(f.ctx, req)
	wantCode(t, err, connect.CodePermissionDenied)
	req.PublisherId = f.publisher.String()
	f.list(t, 0)
	req.SessionId = uuid.NewString()
	_, err = f.server.ListRouteIntents(f.ctx, req)
	wantCode(t, err, connect.CodeFailedPrecondition)
	f.ack(t, p)
	first := f.publications(t)[0]
	f.ack(t, p)
	second := f.publications(t)[0]
	if first.DrainUntil == nil || !first.DrainUntil.Equal(*second.DrainUntil) {
		t.Fatal("duplicate ack extended drain")
	}
	if first.DrainUntil.Sub(*first.AcknowledgedAt) != time.Duration(routingTestDrainSeconds)*time.Second {
		t.Fatalf("drain policy = %v", first.DrainUntil.Sub(*first.AcknowledgedAt))
	}
	report := &routingv1.ReportPublicationRequest{
		PublisherId:   f.publisher.String(),
		SessionId:     f.session,
		PublicationId: p.ID.String(),
		Revision:      p.Revision + 1,
		Outcome:       routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED,
	}
	_, err = f.server.ReportPublication(f.ctx, report)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if _, err = f.pool.Exec(
		t.Context(),
		"UPDATE route_publishers SET session_expires_at=NOW()-INTERVAL '1 second'",
	); err != nil {
		t.Fatal(err)
	}
	report.Revision = p.Revision
	_, err = f.server.ReportPublication(f.ctx, report)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if _, err = f.server.ListRouteIntents(f.ctx, req); err != nil {
		t.Fatal(err)
	}
	report.Revision = p.Revision
	_, err = f.server.ReportPublication(f.ctx, report)
	wantCode(t, err, connect.CodeFailedPrecondition)
	report.SessionId = req.GetSessionId()
	report.Outcome = routingv1.PublicationOutcome_PUBLICATION_OUTCOME_ERROR
	report.Message = "Routing publication failed"
	if _, err = f.server.ReportPublication(f.ctx, report); err != nil {
		t.Fatal(err)
	}
	if got := f.publications(t)[0]; got.AcknowledgedRevision == got.Revision {
		t.Fatal("provider error retained permission to retire")
	}
}

func TestPublicRoutingAdmissionRequiresEveryObservation(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	p := f.placement(t, f.clusterID)
	names := desiredPayload(t, f.deployFixture).AppSpec.ServiceSpec.Routing.Hostnames
	for _, tc := range []struct {
		name     string
		replicas int32
		route    loco.PublicRouteStatus
	}{
		{"zero replicas", 0, loco.PublicRouteStatus{Revision: p.DesiredRevision, Hostnames: names, Ready: true}},
		{"stale revision", 1, loco.PublicRouteStatus{Revision: p.DesiredRevision - 1, Hostnames: names, Ready: true}},
		{"wrong hostname", 1, loco.PublicRouteStatus{
			Revision: p.DesiredRevision, Hostnames: []string{testSecondDomain}, Ready: true,
		}},
		{"missing hostname", 1, loco.PublicRouteStatus{Revision: p.DesiredRevision, Ready: true}},
		{"unaccepted route", 1, loco.PublicRouteStatus{Revision: p.DesiredRevision, Hostnames: names}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.route)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(
				t.Context(),
				"UPDATE placements SET public_route=$2,ready_replicas=$3 WHERE id=$1",
				p.ID,
				encoded,
				tc.replicas,
			); err != nil {
				t.Fatal(err)
			}
			f.reconcile(t)
			if got := f.publications(t)[0].ClusterIds; len(got) != 0 {
				t.Fatalf("advertised ineligible clusters %v", got)
			}
		})
	}
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	admitted := f.publications(t)[0]
	if !slices.Equal(admitted.ClusterIds, []uuid.UUID{f.clusterID}) {
		t.Fatalf("current accepted placement not admitted: %v", admitted.ClusterIds)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE placements SET ready_replicas=0 WHERE id=$1", p.ID); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if got := f.publications(t)[0]; got.Revision != admitted.Revision ||
		!slices.Equal(got.ClusterIds, admitted.ClusterIds) {
		t.Fatalf("replica failure triggered failover: %+v", got)
	}
}

func TestRoutingFeedPaginationChangesAndDomainIsolation(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	for _, name := range []string{"second.example.com", "third.example.com"} {
		if err := f.domain.add(name); err != nil {
			t.Fatal(err)
		}
	}
	f.reconcile(t)
	first := f.list(t, 0)
	if len(first.GetIntents()) != 2 || !first.GetHasMore() {
		t.Fatalf("first page = %+v", first)
	}
	second := f.list(t, first.GetNextSequence())
	if len(second.GetIntents()) != 1 || second.GetHasMore() {
		t.Fatalf("second page = %+v", second)
	}
	seen := map[string]bool{}
	for _, p := range append(first.Intents, second.GetIntents()...) {
		if seen[p.GetHostname()] {
			t.Fatal("duplicate hostname")
		}
		seen[p.GetHostname()] = true
	}
	if len(seen) != 3 || !seen[testPrimaryDomain] {
		t.Fatalf("snapshot = %v", seen)
	}
	cursor := second.GetNextSequence()
	f.reconcile(t)
	if got := f.list(t, cursor); len(got.GetIntents()) != 0 || got.GetNextSequence() != cursor {
		t.Fatalf("unchanged work emitted changes: %+v", got)
	}
	original := f.publications(t)[0]
	f.ack(t, original)
	if got := f.list(t, cursor); len(got.GetIntents()) != 0 || got.GetNextSequence() != cursor {
		t.Fatalf("ack emitted new intent: %+v", got)
	}
	_, err := f.domain.server.UpdateResourceDomain(
		f.domain.ctx,
		&domainv1.UpdateResourceDomainRequest{
			DomainId: f.domain.domainID(t, testPrimaryDomain),
			Domain:   new("renamed.example.com"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	var changes []*routingv1.RouteIntent
	for {
		page := f.list(t, cursor)
		changes = append(changes, page.GetIntents()...)
		cursor = page.GetNextSequence()
		if !page.GetHasMore() {
			break
		}
	}
	if len(changes) != 2 {
		t.Fatalf("rename changes = %v, want old withdrawal and new intent", changes)
	}
	for _, change := range changes {
		if change.GetHostname() == "second.example.com" || change.GetHostname() == "third.example.com" {
			t.Fatalf("unchanged hostname in feed: %s", change.GetHostname())
		}
	}
	other, err := f.server.BindPublisher(
		systemAdminContext(t),
		&routingv1.BindPublisherRequest{AppDomain: "other.example.com", ReservedPrefixes: []string{"reserved-"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	otherID := uuid.MustParse(other.GetPublisherId())
	sequence, err := f.queries.NextRouteChangeSequence(t.Context(), otherID)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, err := f.queries.UpsertRouteIntent(
		t.Context(),
		db.UpsertRouteIntentParams{
			PublisherID:    otherID,
			Hostname:       "svc.other.example.com",
			ResourceID:     uuid.New(),
			EnvironmentID:  uuid.New(),
			ClusterIds:     []uuid.UUID{f.otherCluster},
			ChangeSequence: sequence,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	own := f.list(t, 0)
	for _, intent := range own.GetIntents() {
		if intent.GetPublicationId() == otherPub.ID.String() {
			t.Fatal("publisher read another domain")
		}
	}
	_, err = f.server.ReportPublication(
		f.ctx,
		&routingv1.ReportPublicationRequest{
			PublisherId:   f.publisher.String(),
			SessionId:     f.session,
			PublicationId: otherPub.ID.String(),
			Revision:      otherPub.Revision,
			Outcome:       routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED,
		},
	)
	wantCode(t, err, connect.CodeFailedPrecondition)
	otherCtx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			Scopes: []db.EntityScope{
				{EntityType: db.EntityTypeRoutingPublisher, EntityID: otherID, Scope: db.ScopeRead},
				{EntityType: db.EntityTypeRoutingPublisher, EntityID: otherID, Scope: db.ScopeWrite},
			},
		},
	)
	otherPage, err := f.server.ListRouteIntents(
		otherCtx,
		&routingv1.ListRouteIntentsRequest{PublisherId: other.GetPublisherId(), SessionId: uuid.NewString()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherPage.GetIntents()) != 1 || otherPage.GetIntents()[0].GetPublicationId() != otherPub.ID.String() {
		t.Fatalf("other domain snapshot = %v", otherPage.GetIntents())
	}
}

func TestPublicRoutingDeletionTombstoneAndHostnameReuse(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	published := f.publications(t)[0]
	f.ack(t, published)
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	admin := reqctx.WithPrincipal(t.Context(), reqctx.Principal{UserID: f.userID, Scopes: f.adminScopes(t)})
	if _, err := server.DeleteResource(
		admin,
		&resourcev1.DeleteResourceRequest{ResourceId: f.resourceID.String()},
	); err != nil {
		t.Fatal(err)
	}
	if !f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("explicit deletion did not immediately stop workload")
	}
	f.reconcile(t)
	withdrawal := f.publications(t)[0]
	if !withdrawal.Withdrawing || withdrawal.ID != published.ID {
		t.Fatalf("deleted resource lost tombstone: %+v", withdrawal)
	}
	other := *f.deployFixture
	workspace := f.tenant.workspace(t, "other")
	other.envID = f.tenant.defaultEnvironment(t, workspace)
	other.resourceID = f.tenant.resource(
		t,
		workspace,
		"other",
		&resourcev1.ServiceSpec{Routing: &resourcev1.RoutingConfig{Port: new(int32(8080))}},
	)
	otherDomain := newDomainClient(t, &other)
	wantCode(t, otherDomain.add(testPrimaryDomain), connect.CodeAlreadyExists)
	f.ack(t, withdrawal)
	wantCode(t, otherDomain.add(testPrimaryDomain), connect.CodeAlreadyExists)
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second' WHERE id=$1",
		withdrawal.ID,
	); err != nil {
		t.Fatal(err)
	}
	f.reconciler = publicrouting.New(
		f.pool,
		publicrouting.Config{Interval: time.Second, RetrySeconds: 1, BatchSize: 100},
	)
	f.reconcile(t)
	page := f.list(t, 0)
	if len(page.GetIntents()) != 1 || page.GetIntents()[0].GetPublicationId() != withdrawal.ID.String() ||
		!page.GetIntents()[0].GetWithdrawing() {
		t.Fatalf("drained tombstone disappeared from snapshot: %v", page.GetIntents())
	}
	if err := otherDomain.add(testPrimaryDomain); err != nil {
		t.Fatalf("drained name cannot be reused: %v", err)
	}
	if err := otherDomain.add("loco-net-origin.example.com"); err == nil {
		t.Fatal("reserved infrastructure name assigned")
	}
}

func TestUnboundDomainsKeepImmediateLifecycle(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	domains := newDomainClient(t, f)
	if err := domains.add(testPrimaryDomain); err != nil {
		t.Fatal(err)
	}
	if _, deployErr := createTestDeployment(t, f, sameRegionSpec); deployErr != nil {
		t.Fatal(deployErr)
	}
	_, err := domains.server.UpdateResourceDomain(
		domains.ctx,
		&domainv1.UpdateResourceDomainRequest{
			DomainId: domains.domainID(t, testPrimaryDomain),
			Domain:   new(testSecondDomain),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := desiredPayload(
		t,
		f,
	).AppSpec.ServiceSpec.Routing.Hostnames; !slices.Equal(
		got,
		[]string{testSecondDomain},
	) {
		t.Fatalf("unbound rename retained old name: %v", got)
	}
	if err = withTx(
		t.Context(),
		f.pool,
		func(q *db.Queries) error { return removePlacement(t.Context(), q, f.resourceID, f.envID, f.clusterID) },
	); err != nil {
		t.Fatal(err)
	}
	if !f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("unbound placement waits for publisher")
	}
	r := publicrouting.New(f.pool, publicrouting.Config{Interval: time.Second, RetrySeconds: 1, BatchSize: 100})
	if err = r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := f.queries.ListResourceRoutePublications(
		t.Context(),
		db.ListResourceRoutePublicationsParams{ResourceID: f.resourceID, EnvironmentID: f.envID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("unbound publications = %v", rows)
	}
}

func TestPublicRoutingExactTwoClusterMembershipAndStaleAck(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	ny := f.addOtherRegionCluster(t)
	f.serveDomains(t, testAppDomain)
	if err := f.domain.add(testPrimaryDomain); err != nil {
		t.Fatal(err)
	}
	spec := `{"routing":{"port":8080},"regions":{` +
		`"us-ca-1":{"enabled":true,"cpu":"100m","memory":"64Mi","minReplicas":1,"maxReplicas":1},` +
		`"us-va-1":{"enabled":true,"cpu":"100m","memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
	seedDeployment(t, f.deployFixture, spec)
	_, err := deployRequest(
		t,
		f.deployFixture,
		&deploymentv1.CreateDeploymentRequest{
			ResourceId:    f.resourceID.String(),
			EnvironmentId: f.envID.String(),
			Regions:       []string{testRegion, otherRegion},
			Spec: &deploymentv1.DeploymentSpec{
				Spec: &deploymentv1.DeploymentSpec_Service{
					Service: &deploymentv1.ServiceDeploymentSpec{
						Build: &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage},
						Port:  new(int32(8080)),
					},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	caOnly := f.publications(t)[0]
	f.ack(t, caOnly)
	if !slices.Equal(caOnly.ClusterIds, []uuid.UUID{f.clusterID}) {
		t.Fatalf("CA-only = %v", caOnly.ClusterIds)
	}
	f.ready(t, ny, true)
	f.reconcile(t)
	both := f.publications(t)[0]
	if len(both.ClusterIds) != 2 || !slices.Contains(both.ClusterIds, ny) ||
		!slices.Contains(both.ClusterIds, f.clusterID) {
		t.Fatalf("two clusters = %v", both.ClusterIds)
	}
	f.ack(t, both)
	err = withTx(
		t.Context(),
		f.pool,
		func(q *db.Queries) error { return removePlacement(t.Context(), q, f.resourceID, f.envID, f.clusterID) },
	)
	if err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	nyOnly := f.publications(t)[0]
	if !slices.Equal(nyOnly.ClusterIds, []uuid.UUID{ny}) {
		t.Fatalf("remaining exact cluster = %v", nyOnly.ClusterIds)
	}
	_, err = f.server.ReportPublication(
		f.ctx,
		&routingv1.ReportPublicationRequest{
			PublisherId:   f.publisher.String(),
			SessionId:     f.session,
			PublicationId: both.ID.String(),
			Revision:      both.Revision,
			Outcome:       routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED,
		},
	)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if got := f.publications(t)[0]; got.AcknowledgedRevision == got.Revision {
		t.Fatal("stale ack published new intent")
	}
	if _, err = f.pool.Exec(
		t.Context(),
		"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second'",
	); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("elapsed drain bypassed missing acknowledgement")
	}
	f.ack(t, nyOnly)
	f.reconciler = publicrouting.New(
		f.pool,
		publicrouting.Config{Interval: time.Second, RetrySeconds: 1, BatchSize: 100},
	)
	f.reconcile(t)
	if f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("restart bypassed persisted drain")
	}
	if _, err = f.pool.Exec(
		t.Context(),
		"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second'",
	); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if !f.placement(t, f.clusterID).DesiredDeleted || f.placement(t, ny).DesiredDeleted {
		t.Fatal("retirement did not preserve remaining region")
	}
}

func TestAgentPublicRouteObservationFencesAdmission(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	p := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{
		PlacementId:      p.ID.String(),
		ObservedRevision: p.DesiredRevision,
		Available:        true,
		Healthy:          true,
		Deployed:         true,
		ReadyReplicas:    1,
		PublicRoute: &agentv1.PublicRouteStatus{
			Revision:  p.DesiredRevision,
			Hostnames: []string{testPrimaryDomain},
			Ready:     true,
		},
	}
	agent := f.agentServer()
	agent.recordStatus(t.Context(), f.clusterID, 1, report)
	f.reconcile(t)
	if len(f.publications(t)[0].ClusterIds) != 0 {
		t.Fatal("wrong stream generation admitted route")
	}
	agent.recordStatus(t.Context(), f.clusterID, 0, report)
	f.reconcile(t)
	if !slices.Equal(f.publications(t)[0].ClusterIds, []uuid.UUID{f.clusterID}) {
		t.Fatal("accepted agent report did not admit route")
	}
	if err := f.domain.add(testSecondDomain); err != nil {
		t.Fatal(err)
	}
	agent.recordStatus(t.Context(), f.clusterID, 0, report)
	f.reconcile(t)
	for _, pub := range f.publications(t) {
		if pub.Hostname == testSecondDomain && len(pub.ClusterIds) != 0 {
			t.Fatal("old route observation admitted new alias")
		}
	}
	generation, err := f.queries.BeginClusterSync(t.Context(), f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	current := f.placement(t, f.clusterID)
	report.ObservedRevision = current.DesiredRevision
	report.PublicRoute.Revision = current.DesiredRevision
	report.PublicRoute.Hostnames = desiredPayload(t, f.deployFixture).AppSpec.ServiceSpec.Routing.Hostnames
	agent.recordStatus(t.Context(), f.clusterID, 0, report)
	f.reconcile(t)
	for _, pub := range f.publications(t) {
		if pub.Hostname == testSecondDomain && len(pub.ClusterIds) != 0 {
			t.Fatal("stale session admitted alias")
		}
	}
	agent.recordStatus(t.Context(), f.clusterID, generation, report)
	f.reconcile(t)
	for _, pub := range f.publications(t) {
		if len(pub.ClusterIds) != 1 {
			t.Fatalf("current observation not admitted: %+v", pub)
		}
	}
}

func TestPublicRoutingWorkspaceDeletePreservesTombstones(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	published := f.publications(t)[0]
	f.ack(t, published)
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	admin := reqctx.WithPrincipal(t.Context(), reqctx.Principal{UserID: f.userID, Scopes: f.adminScopes(t)})
	_, err := server.DeleteWorkspace(
		admin,
		&workspacev1.DeleteWorkspaceRequest{WorkspaceId: f.workspaceID(t).String(), ConfirmDeleteApps: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("workspace deletion did not stop workload")
	}
	f.reconcile(t)
	after := f.publications(t)
	if len(after) != 1 || after[0].ID != published.ID || !after[0].Withdrawing {
		t.Fatalf("workspace deletion lost cleanup: %+v", after)
	}
}

func TestRoutingBindingAndReservedNames(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	domains := newDomainClient(t, f)
	if err := domains.add("infra-owned.example.com"); err != nil {
		t.Fatal(err)
	}
	server := NewRoutingServer(f.pool, f.queries, 60, 100, 120)
	_, err := server.BindPublisher(domains.ctx, &routingv1.BindPublisherRequest{AppDomain: testAppDomain})
	wantCode(t, err, connect.CodePermissionDenied)
	_, err = server.BindPublisher(
		systemAdminContext(t),
		&routingv1.BindPublisherRequest{AppDomain: testAppDomain, ReservedPrefixes: []string{"infra-"}},
	)
	wantCode(t, err, connect.CodeFailedPrecondition)
	binding, err := server.BindPublisher(
		systemAdminContext(t),
		&routingv1.BindPublisherRequest{AppDomain: testAppDomain},
	)
	if err != nil {
		t.Fatalf("binding without reservations: %v", err)
	}
	if binding.GetPublisherId() == "" {
		t.Fatal("binding has no identity")
	}
	reconciler := publicrouting.New(
		f.pool,
		publicrouting.Config{Interval: time.Second, RetrySeconds: 1, BatchSize: 100},
	)
	if err = reconciler.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := f.queries.ListResourceRoutePublications(
		t.Context(),
		db.ListResourceRoutePublicationsParams{ResourceID: f.resourceID, EnvironmentID: f.envID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Hostname != "infra-owned.example.com" {
		t.Fatalf("binding did not import existing assignments: %+v", rows)
	}
}

func TestRoutingOwnedTokenHasOnlyPublisherAccess(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	if err := f.queries.AddUserScope(
		t.Context(),
		db.AddUserScopeParams{
			UserID:     f.userID,
			EntityType: db.EntityTypeSystem,
			EntityID:   uuid.Nil,
			Scope:      db.ScopeAdmin,
		},
	); err != nil {
		t.Fatal(err)
	}
	machine := testMachine(f.queries)
	tokens := NewTokenServer(f.pool, f.queries, machine, limits.Defaults())
	admin := scoped(t, f.queries, f.userID)
	created, err := tokens.CreateToken(
		admin,
		tokenRequest(db.Entity{Type: db.EntityTypeRoutingPublisher, ID: f.publisher}, db.ScopeRead, db.ScopeWrite),
	)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := machine.ValidateToken(t.Context(), created.GetToken())
	if err != nil {
		t.Fatal(err)
	}
	if principal.Entity.Type != db.EntityTypeRoutingPublisher || principal.Entity.ID != f.publisher ||
		principal.UserID != uuid.Nil {
		t.Fatalf("publisher principal = %+v", principal.Entity)
	}
	caller := reqctx.WithPrincipal(t.Context(), principal)
	if _, err = f.server.ListRouteIntents(
		caller,
		&routingv1.ListRouteIntentsRequest{PublisherId: f.publisher.String(), SessionId: f.session},
	); err != nil {
		t.Fatal(err)
	}
	_, err = f.server.BindPublisher(caller, &routingv1.BindPublisherRequest{AppDomain: "other.test"})
	wantCode(t, err, connect.CodePermissionDenied)
	resources := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	_, err = resources.GetResource(
		caller,
		&resourcev1.GetResourceRequest{
			Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: f.resourceID.String()},
		},
	)
	wantCode(t, err, connect.CodePermissionDenied)
	reader := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			Scopes: []db.EntityScope{
				{EntityType: db.EntityTypeRoutingPublisher, EntityID: f.publisher, Scope: db.ScopeRead},
			},
		},
	)
	_, err = f.server.ListRouteIntents(
		reader,
		&routingv1.ListRouteIntentsRequest{PublisherId: f.publisher.String(), SessionId: uuid.NewString()},
	)
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestHostnameEditPreservesPendingPlacementRetirement(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	f.ack(t, f.publications(t)[0])
	err := withTx(
		t.Context(),
		f.pool,
		func(q *db.Queries) error { return removePlacement(t.Context(), q, f.resourceID, f.envID, f.clusterID) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.domain.add(testSecondDomain); err != nil {
		t.Fatal(err)
	}
	var retiring bool
	err = f.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM public_routing_retirements
 WHERE resource_id=$1 AND cluster_id=$2)`, f.resourceID, f.clusterID).
		Scan(&retiring)
	if err != nil {
		t.Fatal(err)
	}
	if !retiring {
		t.Fatal("hostname rewrite canceled retirement")
	}
	f.reconcile(t)
	for _, p := range f.publications(t) {
		f.ack(t, p)
	}
	if _, err = f.pool.Exec(
		t.Context(),
		"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second'",
	); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if !f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("hostname edit prevented placement retirement")
	}
}

func TestClusterRemovalReconcilesSettledPublication(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	f.ack(t, f.publications(t)[0])
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second'",
	); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	var queued int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM public_routing_dirty").Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatal("fixture was not settled")
	}
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE clusters SET removed_at=NOW(), is_active=false WHERE id=$1",
		f.clusterID,
	); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if p := f.publications(t)[0]; !p.Withdrawing || len(p.ClusterIds) != 0 {
		t.Fatalf("removed cluster still published: %+v", p)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE clusters SET removed_at=NULL WHERE id=$1", f.clusterID); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if p := f.publications(t)[0]; p.Withdrawing || !slices.Equal(p.ClusterIds, []uuid.UUID{f.clusterID}) {
		t.Fatalf("restored cluster not admitted: %+v", p)
	}
}

func TestPublicationReportCanonicalizesDiagnostics(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	p := f.publications(t)[0]
	f.list(t, 0)
	for _, tc := range []struct {
		outcome       routingv1.PublicationOutcome
		message, want string
	}{
		{routingv1.PublicationOutcome_PUBLICATION_OUTCOME_ERROR, "", "Routing publication failed"},
		{routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED, "Informational provider text", ""},
	} {
		_, err := f.server.ReportPublication(
			f.ctx,
			&routingv1.ReportPublicationRequest{
				PublisherId:   f.publisher.String(),
				SessionId:     f.session,
				PublicationId: p.ID.String(),
				Revision:      p.Revision,
				Outcome:       tc.outcome,
				Message:       tc.message,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.publications(t)[0].Message; got != tc.want {
			t.Fatalf("message=%q want=%q", got, tc.want)
		}
	}
}
