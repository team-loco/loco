package service

import (
	"testing"

	"github.com/team-loco/loco/api/limits"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	routingv1 "github.com/team-loco/loco/gen/go/loco/routing/v1"
)

func TestResourceDomainPublicationTracksCurrentAcknowledgement(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.deploy(t)
	resources := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	check := func(want domainv1.PublicationState) {
		t.Helper()
		response, err := resources.GetResource(
			f.tenant.owner(t),
			&resourcev1.GetResourceRequest{
				Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: f.resourceID.String()},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		domains := response.GetResource().GetDomains()
		if len(domains) != 1 || domains[0].GetPublication().GetState() != want {
			t.Fatalf("domains = %v, want %s", domains, want)
		}
		listed, err := resources.listedResources(f.tenant.owner(t), []resourceKey{{ID: f.resourceID}})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 || listed[0].GetDomains()[0].GetPublication().GetState() != want {
			t.Fatalf("listed publication = %v", listed)
		}
	}
	check(domainv1.PublicationState_PUBLICATION_STATE_PENDING)
	f.reconcile(t)
	check(domainv1.PublicationState_PUBLICATION_STATE_PENDING)
	// Acknowledging an intent with no admitted cluster cannot publish a usable endpoint.
	f.ack(t, f.publications(t)[0])
	check(domainv1.PublicationState_PUBLICATION_STATE_PENDING)
	f.ready(t, f.clusterID, true)
	f.reconcile(t)
	check(domainv1.PublicationState_PUBLICATION_STATE_PENDING)
	p := f.publications(t)[0]
	f.list(t, 0)
	_, err := f.server.ReportPublication(
		f.ctx,
		&routingv1.ReportPublicationRequest{
			PublisherId:   f.publisher.String(),
			SessionId:     f.session,
			PublicationId: p.ID.String(),
			Revision:      p.Revision,
			Outcome:       routingv1.PublicationOutcome_PUBLICATION_OUTCOME_ERROR,
			Message:       "",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	check(domainv1.PublicationState_PUBLICATION_STATE_ERROR)
	_, err = f.server.ReportPublication(f.ctx, &routingv1.ReportPublicationRequest{
		PublisherId: f.publisher.String(), SessionId: f.session, PublicationId: p.ID.String(), Revision: p.Revision,
		Outcome: routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED, Message: "Informational provider text",
	})
	if err != nil {
		t.Fatal(err)
	}
	check(domainv1.PublicationState_PUBLICATION_STATE_PUBLISHED)
}
