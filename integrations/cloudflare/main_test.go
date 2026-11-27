package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	routingv1 "github.com/team-loco/loco/gen/go/loco/routing/v1"
	"github.com/team-loco/loco/gen/go/loco/routing/v1/routingv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

type coreFixture struct {
	routingv1connect.UnimplementedRoutingServiceHandler
	intents                []*routingv1.RouteIntent
	reports                []*routingv1.ReportPublicationRequest
	lists                  int
	pageSize               int
	rejectRenewal, loseAck bool
	rewindOnRenewal        bool
}

func (f *coreFixture) ListRouteIntents(
	_ context.Context,
	req *routingv1.ListRouteIntentsRequest,
) (*routingv1.ListRouteIntentsResponse, error) {
	f.lists++
	if f.rewindOnRenewal && f.lists == 2 {
		f.intents = nil
	}
	if f.rejectRenewal && f.lists > 1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "writer session lost")
	}
	var current int64
	for _, intent := range f.intents {
		current = max(current, intent.GetChangeSequence())
	}
	if req.GetAfterSequence() > current {
		return nil, connect.NewError(connect.CodeInvalidArgument, "routing cursor invalidated")
	}
	response := &routingv1.ListRouteIntentsResponse{NextSequence: req.GetAfterSequence(), SessionSeconds: 60}
	for _, intent := range f.intents {
		if intent.GetChangeSequence() > req.GetAfterSequence() {
			if f.pageSize > 0 && len(response.GetIntents()) == f.pageSize {
				response.HasMore = true
				break
			}
			response.Intents = append(response.Intents, intent)
			response.NextSequence = intent.GetChangeSequence()
		}
	}
	return response, nil
}

func (f *coreFixture) ReportPublication(
	_ context.Context,
	req *routingv1.ReportPublicationRequest,
) (*routingv1.ReportPublicationResponse, error) {
	if f.loseAck {
		f.loseAck = false
		return nil, connect.NewError(connect.CodeUnavailable, "synthetic lost acknowledgement")
	}
	f.reports = append(f.reports, req)
	return &routingv1.ReportPublicationResponse{}, nil
}
func newPublisherFixture(t *testing.T) (*publisher, *coreFixture, *dnsFixture) {
	t.Helper()
	core := &coreFixture{
		intents: []*routingv1.RouteIntent{
			{
				PublicationId:  testPublicationID,
				Revision:       1,
				Hostname:       testHostname,
				ClusterIds:     []string{testCA},
				Preference:     "nearest",
				ChangeSequence: 1,
			},
		},
	}
	mux := http.NewServeMux()
	server := connect.NewServer()
	routingv1connect.RegisterRoutingServiceHandler(server, core)
	connecthttp.Mount(mux, server)
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	dns, client := newDNSFixture(t)
	p := &publisher{
		binding: binding{
			AppDomain:    "apps.example.test",
			PublisherID:  testPublisherID,
			Destinations: []destination{{ClusterIDs: []string{testCA}, Hostname: testTarget}},
		},
		core:         rpc.NewHTTPClients(api.Client(), api.URL, "synthetic-loco-token"),
		dns:          client,
		session:      testNY,
		publications: map[string]*publication{},
	}
	return p, core, dns
}
func TestPublisherAcknowledgesVerifiedStateAndAuditsWithoutExtraWrites(t *testing.T) {
	p, core, dns := newPublisherFixture(t)
	cfg := config{PollSeconds: 1, AuditSeconds: 100, BatchSize: 3}
	budget := cfg.BatchSize
	if _, err := p.runOnce(t.Context(), cfg, &budget); err != nil {
		t.Fatal(err)
	}
	if len(core.reports) != 1 ||
		core.reports[0].GetOutcome() != routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED ||
		dns.writes != 1 {
		t.Fatalf("reports=%v writes=%d", core.reports, dns.writes)
	}
	budget = cfg.BatchSize
	if _, err := p.runOnce(t.Context(), cfg, &budget); err != nil {
		t.Fatal(err)
	}
	if dns.reads != 2 || len(core.reports) != 1 {
		t.Fatal("unchanged feed performed an immediate audit")
	}
}
func TestPublisherLosesLeaseBeforeMutationAndRejectsUnsupportedSet(t *testing.T) {
	p, core, dns := newPublisherFixture(t)
	core.rejectRenewal = true
	budget := 3
	if _, err := p.runOnce(t.Context(), config{PollSeconds: 1, AuditSeconds: 100}, &budget); err == nil {
		t.Fatal("lost lease accepted")
	}
	if dns.writes != 0 || dns.reads != 0 || len(core.reports) != 0 {
		t.Fatal("lost writer touched provider")
	}
	core.rejectRenewal = false
	core.intents[0].ClusterIds = []string{testCA, testNY}
	core.intents[0].Revision++
	core.intents[0].ChangeSequence++
	budget = 3
	if _, err := p.runOnce(t.Context(), config{PollSeconds: 1, AuditSeconds: 100}, &budget); err != nil {
		t.Fatal(err)
	}
	if dns.writes != 0 || len(core.reports) != 1 ||
		core.reports[0].GetOutcome() != routingv1.PublicationOutcome_PUBLICATION_OUTCOME_ERROR {
		t.Fatal("unsupported set changed provider or was acknowledged")
	}
}
func TestPublisherRecoversLostAcknowledgementAndReportsRateLimit(t *testing.T) {
	p, core, dns := newPublisherFixture(t)
	core.loseAck = true
	intent := core.intents[0]
	if err := p.publish(t.Context(), intent); err == nil {
		t.Fatal("lost acknowledgement succeeded")
	}
	if err := p.publish(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	if dns.writes != 1 || len(core.reports) != 1 {
		t.Fatal("acknowledgement retry duplicated DNS write")
	}
	dns.rateLimit = true
	err := p.publish(t.Context(), intent)
	var provider *providerError
	if !errors.As(err, &provider) || provider.status != http.StatusTooManyRequests {
		t.Fatalf("rate limit=%v", err)
	}
	if core.reports[len(core.reports)-1].GetOutcome() != routingv1.PublicationOutcome_PUBLICATION_OUTCOME_ERROR {
		t.Fatal("rate limit did not block acknowledgement")
	}
}

func TestPublisherResnapshotsInvalidatedCursor(t *testing.T) {
	p, core, dns := newPublisherFixture(t)
	p.cursor = 99
	p.publications[testNY] = &publication{intent: &routingv1.RouteIntent{PublicationId: testNY, Revision: 9}}
	budget := 3
	if _, err := p.runOnce(t.Context(), config{PollSeconds: 1, AuditSeconds: 100}, &budget); err != nil {
		t.Fatal(err)
	}
	if p.cursor != 1 || len(p.publications) != 1 || p.publications[testNY] != nil {
		t.Fatal("invalidated snapshot was retained")
	}
	if dns.writes != 1 || len(core.reports) != 1 {
		t.Fatal("resnapshot did not resume publication")
	}
}
func TestPublisherLoadsAllPagesAndDoesNotStarveBehindFailures(t *testing.T) {
	p, core, dns := newPublisherFixture(t)
	core.pageSize = 1
	core.intents = nil
	for i, id := range []string{testPublicationID, testPublisherID, testNY} {
		core.intents = append(
			core.intents,
			&routingv1.RouteIntent{
				PublicationId:  id,
				Revision:       1,
				Hostname:       "web-" + strconv.Itoa(i) + ".apps.example.test",
				ClusterIds:     []string{testCA, testNY},
				Preference:     "nearest",
				ChangeSequence: int64(i + 1),
			},
		)
	}
	for range 3 {
		budget := 1
		if _, err := p.runOnce(t.Context(), config{PollSeconds: 60, AuditSeconds: 100}, &budget); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, report := range core.reports {
		seen[report.GetPublicationId()] = true
	}
	if len(seen) != 3 || len(p.publications) != 3 || p.cursor != 3 || dns.writes != 0 {
		t.Fatalf("reports=%v cursor=%d", core.reports, p.cursor)
	}
}

func TestPublisherSkipsIntentRemovedDuringLeaseRenewal(t *testing.T) {
	p, core, dns := newPublisherFixture(t)
	core.rewindOnRenewal = true
	budget := 3
	if _, err := p.runOnce(t.Context(), config{PollSeconds: 1, AuditSeconds: 100}, &budget); err != nil {
		t.Fatal(err)
	}
	if len(p.publications) != 0 || p.cursor != 0 || dns.writes != 0 || len(core.reports) != 0 {
		t.Fatal("removed intent still published after resnapshot")
	}
}
