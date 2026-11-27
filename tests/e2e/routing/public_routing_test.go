package routing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	routingv1 "github.com/team-loco/loco/gen/go/loco/routing/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const routeWait = 2 * time.Minute

// These requests cross two independent Kubernetes APIs, controllers and Envoys.
// Publication acknowledgements here are synthetic; no external DNS provider is used.
func TestPublicRoutingAcrossTwoWorkers(t *testing.T) {
	if err := p.ForwardGateway(t.Context()); err != nil {
		t.Fatal(err)
	}
	second := p.AddWorker(t, &clusterv1.NewRegion{Country: "us", Subdivision: "va"})
	binding, err := p.Admin.Routing.BindPublisher(
		t.Context(),
		&routingv1.BindPublisherRequest{AppDomain: p.Domain, ReservedPrefixes: []string{"infra-"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	w := p.NewTenant(t)
	service := func(regions ...string) locofile.Service {
		s := platform.ImageService(p.PublicImage)
		s.Command = []string{"/bin/sh"}
		s.Args = []string{"-c", `cat > /tmp/loco-region.conf <<NGINX
pid /tmp/nginx.pid;
error_log /dev/stderr;
events {}
http {
  access_log off;
  server {
    listen 8080;
    location / { return 200 "$LOCO_REGION"; }
  }
}
NGINX
exec nginx -c /tmp/loco-region.conf -g 'daemon off;'`}

		s.Regions = make(map[string]locofile.Region, len(regions))
		for _, region := range regions {
			s.Regions[region] = platform.OneReplica()
		}
		return s
	}
	ca, ny, both := service(p.Region), service(second.Region), service(p.Region, second.Region)
	ca.Domains = []string{w.Host("ca")}
	ny.Domains = []string{w.Host("ny")}
	both.Domains = []string{w.Host("both")}
	dir := t.TempDir()
	platform.WriteLocoFile(
		t,
		dir,
		locofile.File{Partial: "regional", Services: map[string]locofile.Service{"ca": ca, "ny": ny, "both": both}},
	)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	session := uuid.NewString()
	var cursor int64
	intents := map[string]*routingv1.RouteIntent{}
	expected := map[string][]string{
		w.Host("ca"):   {p.ClusterID},
		w.Host("ny"):   {second.ClusterID},
		w.Host("both"): {p.ClusterID, second.ClusterID},
	}
	platform.Eventually(t, routeWait, "exact public cluster membership", func(ctx context.Context) error {
		page, err := p.Admin.Routing.ListRouteIntents(
			ctx,
			&routingv1.ListRouteIntentsRequest{
				PublisherId:   binding.GetPublisherId(),
				SessionId:     session,
				AfterSequence: cursor,
			},
		)
		if err != nil {
			return err
		}
		for _, intent := range page.GetIntents() {
			intents[intent.GetHostname()] = intent
		}
		cursor = page.GetNextSequence()
		for hostname, clusters := range expected {
			intent := intents[hostname]
			if intent == nil {
				return fmt.Errorf("no intent for %s", hostname)
			}
			got := slices.Clone(intent.GetClusterIds())
			want := slices.Clone(clusters)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				return fmt.Errorf("%s clusters=%v want=%v", hostname, got, want)
			}
		}
		return nil
	})
	for hostname, intent := range intents {
		if _, ok := expected[hostname]; !ok {
			continue
		}
		if _, err := p.Admin.Routing.ReportPublication(
			t.Context(),
			&routingv1.ReportPublicationRequest{
				PublisherId:   binding.GetPublisherId(),
				SessionId:     session,
				PublicationId: intent.GetPublicationId(),
				Revision:      intent.GetRevision(),
				Outcome:       routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, worker := range []*platform.Platform{p, second} {
		for hostname, clusters := range expected {
			status := http.StatusNotFound
			body := ""
			if slices.Contains(clusters, worker.ClusterID) {
				status = http.StatusOK
				body = worker.Region
			}
			platform.Eventually(t, routeWait, hostname+" via "+worker.Region, func(ctx context.Context) error {
				response, err := worker.GatewayHTTPSResponse(ctx, hostname, "/")
				if err != nil {
					return err
				}
				data, readErr := io.ReadAll(response.Body)
				closeErr := response.Body.Close()
				if readErr != nil {
					return readErr
				}
				if closeErr != nil {
					return closeErr
				}
				if response.StatusCode != status {
					return fmt.Errorf(
						"%s via %s: HTTP %d, want %d",
						hostname,
						worker.Region,
						response.StatusCode,
						status,
					)
				}
				if status == http.StatusOK && string(data) != body {
					return fmt.Errorf("%s via %s returned %q", hostname, worker.Region, data)
				}
				return nil
			})
		}
	}
	t.Log(
		"Single-region and shared hostnames returned the expected worker identities over verified HTTPS",
	)
}
