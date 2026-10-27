package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testNamespace     = "loco-system"
	testGateway       = "eg"
	testOrgID         = "0198c3c4-0000-7000-8000-00000000000a"
	testWorkspaceID   = "0198c3c4-0000-7000-8000-000000000001"
	testResourceID    = "0198c3c4-0000-7000-8000-00000000000b"
	testEnvironmentID = "0198c3c4-0000-7000-8000-00000000000c"
	testProxyUID      = "0198c3c4-0000-7000-8000-0000000000aa"
	testOtherProxyUID = "0198c3c4-0000-7000-8000-0000000000bb"
	testRouteBytes    = 4096
	testScrapeTimeout = time.Second
)

var (
	errTestScrape = errors.New("port-forward refused")
	testRoute     = "httproute/ws-" + testWorkspaceID + "/resource-" + testResourceID + "-route/rule/0"
)

func adminStats(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestParseRouteBytesReadsOnlyRouteClusters(t *testing.T) {
	stats := adminStats(
		"cluster."+testRoute+".upstream_cx_rx_bytes_total: 4096",
		"cluster."+testRoute+".upstream_cx_tx_bytes_total: 512",
		"cluster.xds_cluster.upstream_cx_rx_bytes_total: 19227",
		"http.http-10080.downstream_cx_tx_bytes_total: 9999",
	)
	routes, err := ParseRouteBytes(strings.NewReader(stats))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(routes) != 2 || routes[testRoute] != testRouteBytes || routes["xds_cluster"] != 19227 {
		t.Fatalf("routes = %v, want the rx bytes of each cluster", routes)
	}
}

func TestParseRouteBytesRejectsANonNumericCounter(t *testing.T) {
	stats := adminStats("cluster." + testRoute + ".upstream_cx_rx_bytes_total: lots")
	if _, err := ParseRouteBytes(strings.NewReader(stats)); err == nil {
		t.Fatal("parsed a counter that is not a number")
	}
}

func TestFetchRouteBytesAsksTheAdminForRouteCountersOnly(t *testing.T) {
	var filter string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter = r.URL.Query().Get("filter")
		stats := adminStats("cluster." + testRoute + ".upstream_cx_rx_bytes_total: 4096")
		if _, err := w.Write([]byte(stats)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	routes, err := fetchRouteBytes(t.Context(), port)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if filter != statsFilter || routes[testRoute] != testRouteBytes {
		t.Fatalf("filter %q returned %v", filter, routes)
	}
}

type scrapes map[types.UID]map[string]int64

func (s scrapes) RouteBytes(_ context.Context, pod *corev1.Pod) (map[string]int64, error) {
	routes, ok := s[pod.UID]
	if !ok {
		return nil, errTestScrape
	}
	return routes, nil
}

type applications []locov1alpha1.Application

func (a applications) Applications(context.Context) ([]locov1alpha1.Application, error) {
	return a, nil
}

func proxy(uid string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		Name:      "envoy-" + uid,
		Namespace: testNamespace,
		UID:       types.UID(uid),
		Labels: map[string]string{
			labelOwningGatewayName:      testGateway,
			labelOwningGatewayNamespace: testNamespace,
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func application() locov1alpha1.Application {
	return locov1alpha1.Application{
		Name:      "resource-" + testResourceID,
		Namespace: testNamespace,
		Spec: locov1alpha1.ApplicationSpec{
			ResourceID:     testResourceID,
			OrganizationID: testOrgID,
			WorkspaceID:    testWorkspaceID,
			EnvironmentID:  testEnvironmentID,
		},
	}
}

func newTestMeter(scraped scrapes, apps applications, pods ...*corev1.Pod) *Meter {
	objects := make([]runtime.Object, len(pods))
	for i, pod := range pods {
		objects[i] = pod
	}
	client := fake.NewClientset(objects...)
	cfg := Config{Namespace: testNamespace, Gateway: testGateway, ScrapeTimeout: testScrapeTimeout}
	return New(client, cfg, apps, scraped)
}

func TestCountersAttributesEachRouteToItsApplication(t *testing.T) {
	foreign := "httproute/ws-other/resource-" + testResourceID + "-route/rule/0"
	unknown := "httproute/ws-" + testWorkspaceID + "/resource-0198c3c4-0000-7000-8000-0000000000ff-route/rule/0"
	scraped := scrapes{testProxyUID: {
		testRoute:     testRouteBytes,
		foreign:       1,
		unknown:       1,
		"xds_cluster": 1,
	}}
	m := newTestMeter(scraped, applications{application()}, proxy(testProxyUID, corev1.PodRunning))

	counters, complete := m.Counters(t.Context())
	if !complete {
		t.Fatal("a report that read every proxy is not complete")
	}
	if len(counters) != 1 {
		t.Fatalf("counters = %v, want only the route of a known application in its workspace", counters)
	}
	counter := counters[0]
	if counter.GetGatewayPodUid() != testProxyUID || counter.GetRoute() != testRoute ||
		counter.GetBytes() != testRouteBytes || counter.GetOrganizationId() != testOrgID ||
		counter.GetWorkspaceId() != testWorkspaceID || counter.GetResourceId() != testResourceID ||
		counter.GetEnvironmentId() != testEnvironmentID {
		t.Fatalf("counter = %v", counter)
	}
}

func TestCountersIsIncompleteWhenAProxyCannotBeRead(t *testing.T) {
	scraped := scrapes{testProxyUID: {testRoute: testRouteBytes}}
	m := newTestMeter(scraped, applications{application()},
		proxy(testProxyUID, corev1.PodRunning),
		proxy(testOtherProxyUID, corev1.PodRunning),
	)
	counters, complete := m.Counters(t.Context())
	if complete {
		t.Fatal("a report missing a proxy is complete")
	}
	if len(counters) != 1 || counters[0].GetGatewayPodUid() != testProxyUID {
		t.Fatalf("counters = %v, want the readable proxy's counters", counters)
	}
}

func TestCountersSkipsProxiesThatAreNotRunning(t *testing.T) {
	m := newTestMeter(scrapes{}, applications{application()}, proxy(testProxyUID, corev1.PodPending))
	counters, complete := m.Counters(t.Context())
	if !complete || len(counters) != 0 {
		t.Fatalf("counters = %v complete = %t, want nothing and complete", counters, complete)
	}
}

func TestCountersSkipsAnApplicationWithoutAnEnvironment(t *testing.T) {
	app := application()
	app.Spec.EnvironmentID = ""
	scraped := scrapes{testProxyUID: {testRoute: testRouteBytes}}
	m := newTestMeter(scraped, applications{app}, proxy(testProxyUID, corev1.PodRunning))
	if counters, _ := m.Counters(t.Context()); len(counters) != 0 {
		t.Fatalf("counters = %v, want nothing for an application without an environment", counters)
	}
}
