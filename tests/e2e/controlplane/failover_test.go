package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	failoverImage       = "loco-api-failover:e2e"
	fixtureLabel        = "e2e.loco.io/fixture"
	healthyPort         = 18080
	closedPort          = 18081
	envoyAdminPort      = 19000
	programTimeout      = 90 * time.Second
	payloadBytes        = 8192
	largePayloadBytes   = 2 * 1024 * 1024
	requestsPerStatus   = 12
	mixedRequests       = 40
	retrySuccessStat    = "upstream_rq_retry_success"
	upstreamConnectFail = "upstream connect error"
	apiSuffix           = "-api"
	uiSuffix            = "-ui"
)

// safeRetryTriggers are the failures the API's BackendTrafficPolicy retries: none of them reaches a
// replica, so a retry cannot repeat a request.
var safeRetryTriggers = []string{"connect-failure", "refused-stream", "reset-before-request"}

var errNotProgrammed = errors.New("not programmed yet")

type httpRoute struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Hostnames []string `json:"hostnames"`
		Rules     []struct {
			Name    string `json:"name"`
			Matches []struct {
				Path struct {
					Value string `json:"value"`
				} `json:"path"`
			} `json:"matches"`
		} `json:"rules"`
	} `json:"spec"`
}

// failover is one run of the check: a fixture pod behind a healthy and a closed endpoint of each
// rendered layout's API and UI Services, and the Envoy admin API of the chart's Gateway.
type failover struct {
	cp     *controlPlane
	name   string
	ip     string
	admin  string
	direct string
	owned  [][2]string
}

// checkFailover renders the chart's routes in three layouts (no UI, the UI on the API host, the UI
// on its own host) against Services whose endpoints are the fixture and a closed port. Without
// the chart's BackendTrafficPolicy some requests fail to connect; with it Envoy retries connection
// failures once and no other failure, and the fixture's ledger shows every request it handled
// reached it exactly once with its whole body.
//
//nolint:thelper // a step of TestControlPlane
func (cp *controlPlane) checkFailover(t *testing.T) {
	f := &failover{cp: cp, name: "api-failover-" + randomHex(t, 4)}
	t.Cleanup(f.cleanup(t))
	f.startFixture(t)
	var worker struct {
		Global struct {
			Domain struct {
				Platform string `json:"platform"`
			} `json:"domain"`
		} `json:"global"`
	}
	mustReadYAML(t, c.Path("tests", "e2e", "worker-values.yaml"), &worker)
	for _, layout := range []string{"headless", "shared", "separate"} {
		t.Run(layout, func(t *testing.T) { f.checkLayout(t, layout, worker.Global.Domain.Platform) })
	}
}

func (f *failover) startFixture(t *testing.T) {
	t.Helper()
	f.apply(t, "Pod", f.name, fmt.Sprintf(`spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: server
      image: %s
      imagePullPolicy: IfNotPresent
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
      ports:
        - name: http
          containerPort: %d
      readinessProbe:
        httpGet:
          path: /
          port: http
      resources:
        requests:
          cpu: 25m
          memory: 16Mi
        limits:
          memory: 64Mi
`, failoverImage, healthyPort))
	f.apply(t, "NetworkPolicy", f.name, fmt.Sprintf(`spec:
  podSelector:
    matchLabels:
      %s: %s
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: %s
          podSelector:
            matchLabels:
              app.kubernetes.io/component: proxy
              app.kubernetes.io/managed-by: envoy-gateway
      ports:
        - protocol: TCP
          port: %d
        - protocol: TCP
          port: %d
`, fixtureLabel, f.name, f.cp.namespace, healthyPort, closedPort))
	ns := f.cp.namespace
	c.MustKubectl(t, "-n", ns, "wait", "pod/"+f.name, "--for=condition=Ready", "--timeout=120s")
	f.ip = strings.TrimSpace(c.MustKubectl(t, "-n", ns, "get", "pod", f.name, "-o", "jsonpath={.status.podIP}"))
	proxies := strings.Fields(c.MustKubectl(t, "-n", ns, "get", "pods", "-l",
		"gateway.envoyproxy.io/owning-gateway-name="+f.cp.values.Gateway.Name+",app.kubernetes.io/component=proxy",
		"-o", "name"))
	if len(proxies) != 1 {
		t.Fatalf("the Gateway runs %d Envoy proxies, want 1", len(proxies))
	}
	f.admin = "http://127.0.0.1:" + strconv.Itoa(c.PortForward(t, ns, proxies[0], envoyAdminPort))
	f.direct = "http://127.0.0.1:" + strconv.Itoa(c.PortForward(t, ns, "pod/"+f.name, healthyPort))
}

// apply applies an object the check owns, labeled for the cleanup to find; body is the YAML after
// its metadata.
func (f *failover) apply(t *testing.T, kind, name, body string, labels ...string) {
	t.Helper()
	apiVersion := map[string]string{"NetworkPolicy": "networking.k8s.io/v1", "EndpointSlice": "discovery.k8s.io/v1"}[kind]
	if apiVersion == "" {
		apiVersion = "v1"
	}
	f.own(kind, name)
	c.MustApply(t, fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata:\n  name: %s\n  namespace: %s\n  labels:\n"+
		"    %s: %s\n%s%s", apiVersion, kind, name, f.cp.namespace, fixtureLabel, f.name,
		strings.Join(labels, ""), body))
}

// applyRendered applies a rendered object the check owns, labeled for the cleanup to find.
func (f *failover) applyRendered(t *testing.T, object map[string]any) {
	t.Helper()
	var meta struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	roundTrip(t, object, &meta)
	if meta.Metadata.Labels == nil {
		meta.Metadata.Labels = map[string]string{}
	}
	meta.Metadata.Labels[fixtureLabel] = f.name
	labeled := maps.Clone(object)
	metadata := map[string]any{}
	roundTrip(t, object["metadata"], &metadata)
	metadata["namespace"] = f.cp.namespace
	metadata["labels"] = meta.Metadata.Labels
	labeled["metadata"] = metadata
	f.own(meta.Kind, meta.Metadata.Name)
	applyJSON(t, labeled)
}

func (f *failover) own(kind, name string) {
	if object := [2]string{kind, name}; !slices.Contains(f.owned, object) {
		f.owned = append(f.owned, object)
	}
}

// cleanup deletes what the check created and fails the test when any of it remains.
func (f *failover) cleanup(t *testing.T) func() {
	t.Helper()
	return func() {
		ctx := context.WithoutCancel(t.Context())
		for _, owned := range slices.Backward(f.owned) {
			if _, err := c.Kubectl(ctx, "-n", f.cp.namespace, "delete", owned[0], owned[1],
				"--ignore-not-found=true", "--wait=true", "--timeout=90s"); err != nil {
				t.Error(err)
			}
		}
		remaining, err := c.Kubectl(ctx, "-n", f.cp.namespace, "get",
			"pod,service,endpointslice,httproute,backendtrafficpolicy,networkpolicy", "-l", fixtureLabel+"="+f.name,
			"-o", "name")
		if err != nil {
			t.Error(err)
		} else if strings.TrimSpace(remaining) != "" {
			t.Errorf("the cleanup left %s", remaining)
		}
	}
}

// layout is one rendering of the chart's routes and the requests sent through them.
type layout struct {
	f        *failover
	release  string
	routes   []httpRoute
	objects  []map[string]any
	traffic  map[string]any
	names    []string
	rules    int
	targets  []target
	clusters map[string]string
	requests []sent
}

// target is an API route's hostname and path prefix.
type target struct{ host, prefix string }

//nolint:thelper // a step of TestControlPlane
func (f *failover) checkLayout(t *testing.T, name, domain string) {
	l := f.render(t, name, domain)
	l.program(t)
	t.Cleanup(func() {
		if t.Failed() {
			ctx := context.WithoutCancel(t.Context())
			configs, err := f.routeConfigs(ctx, l.names)
			t.Logf("route configurations: %v %v", configs, err)
			ledger, err := getJSON[map[string]any](ctx, f.direct+"/state")
			t.Logf("fixture ledger: %v %v", ledger, err)
		}
	})
	before := l.waitForRoutes(t, "the healthy and closed endpoints of every route", func(configs routeSet) bool {
		return f.endpointsEach(t, configs, 2)
	})
	for name, route := range before {
		if _, ok := retryPolicy(t, route); ok {
			t.Fatalf("route %s retries before the BackendTrafficPolicy applies", name)
		}
	}
	l.findAPIClusters(t, before)
	baseline := map[string]int{}
	for _, target := range l.targets {
		baseline[target.host] = l.connectionFailures(t, target, "red")
		if baseline[target.host] == 0 || baseline[target.host] == mixedRequests {
			t.Fatalf("%d of %d requests to %s failed to connect without the policy, want some", baseline[target.host],
				mixedRequests, target.host)
		}
	}

	f.applyRendered(t, l.traffic)
	after := l.waitForRoutes(t, "the API retry policy", func(configs routeSet) bool {
		api := 0
		for _, route := range configs {
			if isAPIRoute(t, route) {
				api++
				if _, ok := retryPolicy(t, route); !ok {
					return false
				}
			}
		}
		return api > 0
	})
	l.checkPolicy(t, before, after)
	statsBefore := f.retryStats(t, l.clusters)
	for _, target := range l.targets {
		if failures := l.connectionFailures(t, target, "new"); failures*2 > baseline[target.host] {
			t.Errorf("%d of %d requests to %s failed to connect with the policy, %d without; want at most half",
				failures, mixedRequests, target.host, baseline[target.host])
		}
	}
	statsAfter := f.retryStats(t, l.clusters)
	for host := range l.clusters {
		if statsAfter[host] <= statsBefore[host] {
			t.Errorf("Envoy retried no request to %s successfully (%d before, %d after)", host, statsBefore[host],
				statsAfter[host])
		}
	}

	for _, suffix := range []string{apiSuffix, uiSuffix} {
		c.MustKubectl(t, "-n", f.cp.namespace, "delete", "endpointslice", l.release+suffix+"-closed", "--wait=true")
	}
	platform.Eventually(t, programTimeout, "the closed endpoints leaving every route", func(context.Context) error {
		if !f.endpointsEach(t, after, 1) {
			return errNotProgrammed
		}
		return nil
	})
	l.checkHandledOnce(t)
	t.Logf("%d requests; connection failures without the policy %v", len(l.requests), baseline)
}

// render renders the chart's routes for a layout: no UI, the UI on the API host, or the UI on its
// own host.
func (f *failover) render(t *testing.T, name, domain string) *layout {
	t.Helper()
	l := &layout{f: f, release: f.name + "-" + name}
	host := l.release + "." + domain
	uiHost := "ui-" + host
	if name == "shared" {
		uiHost = host
	}
	values := writeFile(t, name+"-values.yaml", fmt.Sprintf(`gatewayNamespace: %s
controlPlaneHostname: %s
gateway:
  name: %s
certManager:
  enabled: false
api:
  image:
    tag: e2e
  existingSecret: unused
  clickhouse:
    existingSecret: {name: unused, key: url}
openbao:
  enabled: false
  external:
    address: http://unused:8200
obsProxy:
  image:
    tag: e2e
  ingest:
    clickhouse:
      existingSecret: {name: unused, key: url}
      migratorExistingSecret: {name: unused, key: url}
ui:
  enabled: %t
  hostname: %s
  image:
    tag: e2e
`, f.cp.namespace, host, f.cp.values.Gateway.Name, name != "headless", uiHost))
	var traffic, gateways []map[string]any
	for _, document := range renderedDocuments(t, helm(t, "template", l.release, c.Path("charts", "loco-control-plane"),
		"-n", f.cp.namespace, "-f", values)) {
		switch document["kind"] {
		case "HTTPRoute":
			var route httpRoute
			roundTrip(t, document, &route)
			l.routes = append(l.routes, route)
			l.objects = append(l.objects, document)
			l.names = append(l.names, route.Metadata.Name)
			l.rules += len(route.Spec.Rules)
		case "BackendTrafficPolicy":
			traffic = append(traffic, document)
		case "Gateway":
			gateways = append(gateways, document)
		default:
		}
	}
	if len(traffic) != 1 || len(gateways) != 1 {
		t.Fatalf(
			"the chart renders %d BackendTrafficPolicies and %d Gateways, want one each",
			len(traffic),
			len(gateways),
		)
	}
	l.traffic = traffic[0]
	applyJSON(t, gateways[0])
	return l
}

// program points the layout's API and UI Services at the fixture and a closed port, and applies
// its routes.
func (l *layout) program(t *testing.T) {
	t.Helper()
	for _, suffix := range []string{apiSuffix, uiSuffix} {
		service := l.release + suffix
		l.f.apply(t, "Service", service, "spec:\n  ports:\n    - name: http\n      port: 80\n")
		for endpoint, port := range map[string]int{"-healthy": healthyPort, "-closed": closedPort} {
			l.f.apply(t, "EndpointSlice", service+endpoint, fmt.Sprintf(`addressType: IPv4
ports:
  - name: http
    protocol: TCP
    port: %d
endpoints:
  - addresses: [%s]
    conditions:
      ready: true
`, port, l.f.ip), "    kubernetes.io/service-name: "+service+"\n")
		}
	}
	for _, route := range l.objects {
		l.f.applyRendered(t, route)
	}
}

// routeSet maps Envoy route configuration names to the configurations.
type routeSet map[string]map[string]any

// waitForRoutes waits until Envoy programs every rule of the layout's routes and ready accepts them.
func (l *layout) waitForRoutes(t *testing.T, what string, ready func(routeSet) bool) routeSet {
	t.Helper()
	var programmed routeSet
	platform.Eventually(t, programTimeout, what, func(ctx context.Context) error {
		configs, err := l.f.routeConfigs(ctx, l.names)
		if err != nil {
			return err
		}
		if len(configs) != l.rules || !ready(configs) {
			return errNotProgrammed
		}
		programmed = configs
		return nil
	})
	return programmed
}

// findAPIClusters finds the API routes and the Envoy cluster of each, which must differ.
func (l *layout) findAPIClusters(t *testing.T, routes routeSet) {
	t.Helper()
	for _, route := range l.routes {
		for _, rule := range route.Spec.Rules {
			if rule.Name == "api" {
				l.targets = append(
					l.targets,
					target{route.Spec.Hostnames[0], strings.TrimSuffix(rule.Matches[0].Path.Value, "/")},
				)
			}
		}
	}
	l.clusters = map[string]string{}
	for _, target := range l.targets {
		suffix := "/" + strings.ReplaceAll(target.host, ".", "_")
		for name, route := range routes {
			if strings.HasSuffix(name, suffix) && isAPIRoute(t, route) {
				l.clusters[target.host] = routeCluster(t, route)
				break
			}
		}
	}
	if distinct := slices.Compact(slices.Sorted(maps.Values(l.clusters))); len(distinct) != len(l.targets) {
		t.Fatalf("the API routes %v share Envoy clusters %v", l.targets, l.clusters)
	}
}

// checkPolicy checks that the policy added only a retry on safe failures to the API routes.
func (l *layout) checkPolicy(t *testing.T, before, after routeSet) {
	t.Helper()
	var policy struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	roundTrip(t, l.traffic, &policy)
	for name, route := range after {
		clean := map[string]any{}
		roundTrip(t, route, &clean)
		retry, retries := retryPolicy(t, clean)
		if action, ok := clean["route"].(map[string]any); ok {
			delete(action, "retry_policy")
		}
		if retries {
			gateway := filterMetadata(clean)
			provenance := gateway["policies"]
			delete(gateway, "policies")
			want := []any{map[string]any{
				"namespace": l.f.cp.namespace, "kind": "BackendTrafficPolicy", "name": policy.Metadata.Name,
			}}
			if !reflect.DeepEqual(provenance, want) {
				t.Errorf("route %s names the policies %v, want %v", name, provenance, want)
			}
			triggers := strings.Split(fmt.Sprint(retry["retry_on"]), ",")
			slices.Sort(triggers)
			if !slices.Equal(triggers, safeRetryTriggers) || fmt.Sprint(retry["num_retries"]) != "1" ||
				retry["retriable_status_codes"] != nil || retry["per_try_timeout"] != nil {
				t.Errorf("route %s retries with %v, want one retry on %v only", name, retry, safeRetryTriggers)
			}
		}
		if !reflect.DeepEqual(clean, before[name]) {
			t.Errorf(
				"the policy changed route %s beyond its retry policy:\nbefore %v\nafter  %v",
				name,
				before[name],
				clean,
			)
		}
	}
}

// connectionFailures sends requests to a route whose endpoints include the closed port, and counts
// those that never reached the fixture; each either reached it or failed to connect.
func (l *layout) connectionFailures(t *testing.T, target target, phase string) int {
	t.Helper()
	failures := 0
	for index := range mixedRequests {
		request := l.send(t, target, phase, "ok", index, payloadBytes)
		switch {
		case request.handled && request.status == http.StatusOK:
		case !request.handled && request.status == http.StatusServiceUnavailable &&
			strings.Contains(request.body, upstreamConnectFail):
			failures++
		default:
			t.Fatalf("%s: status %d, handled %t: %s", request.id, request.status, request.handled, request.body)
		}
	}
	return failures
}

// checkHandledOnce sends requests the fixture answers with 200, 500 and 503, small and large, once
// only the healthy endpoint remains, and checks that the fixture received every request it handled
// exactly once with its whole body, and none it did not.
func (l *layout) checkHandledOnce(t *testing.T) {
	t.Helper()
	for _, target := range l.targets {
		for _, status := range []struct {
			path     string
			expected int
		}{{"ok", http.StatusOK}, {"error", http.StatusInternalServerError}, {"handled-503", http.StatusServiceUnavailable}} {
			for index := range requestsPerStatus + 1 {
				size := payloadBytes
				phase := "healthy"
				if index == requestsPerStatus {
					size, phase = largePayloadBytes, "large"
				}
				request := l.send(t, target, phase, status.path, index%requestsPerStatus, size)
				if !request.handled || request.status != status.expected {
					t.Fatalf("%s: status %d, handled %t, want %d handled", request.id, request.status, request.handled,
						status.expected)
				}
			}
		}
	}
	ledger, err := getJSON[map[string][]receipt](t.Context(), l.f.direct+"/state")
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range l.requests {
		var want []receipt
		if request.handled {
			want = []receipt{request.receipt}
		}
		if got := ledger[request.id]; !slices.Equal(got, want) {
			t.Errorf("the fixture received %s as %v, want %v", request.id, got, want)
		}
	}
}

func (l *layout) send(t *testing.T, target target, phase, path string, index, size int) sent {
	t.Helper()
	request := l.f.send(t, target.host, target.prefix, phase, path, index, size)
	l.requests = append(l.requests, request)
	return request
}

// endpointsEach reports whether the Envoy cluster of every route has count endpoints.
func (f *failover) endpointsEach(t *testing.T, routes routeSet, count int) bool {
	t.Helper()
	hosts, err := f.clusterHosts(t.Context())
	if err != nil {
		t.Log(err)
		return false
	}
	for _, route := range routes {
		if hosts[routeCluster(t, route)] != count {
			return false
		}
	}
	return true
}

type receipt struct {
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type sent struct {
	receipt

	id      string
	status  int
	handled bool
	body    string
}

// send posts a request with an ID to the fixture through the Gateway, and reports whether the
// fixture handled it.
func (f *failover) send(t *testing.T, host, prefix, phase, path string, index, size int) sent {
	t.Helper()
	id := fmt.Sprintf("%s-%s-%s-%03d", host, phase, path, index)
	raw, err := json.Marshal(map[string]string{"id": id, "payload": strings.Repeat("x", size)})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	response := f.cp.request(t, http.MethodPost, host, prefix+"/"+path, string(raw), nil)
	body := response.body
	var echoed struct {
		ID string `json:"id"`
	}
	handled := json.Unmarshal(body, &echoed) == nil && echoed.ID == id
	return sent{
		receipt: receipt{len(raw), hex.EncodeToString(digest[:])},
		id:      id, status: response.status, handled: handled, body: string(body),
	}
}

// routeConfigs finds Envoy's route configurations of the named HTTPRoutes, keyed by name.
func (f *failover) routeConfigs(ctx context.Context, names []string) (map[string]map[string]any, error) {
	dump, err := getJSON[any](ctx, f.admin+"/config_dump?resource=dynamic_route_configs")
	if err != nil {
		return nil, err
	}
	configs := map[string]map[string]any{}
	var walk func(value any)
	walk = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			name := fmt.Sprint(value["name"])
			if _, ok := value["route"]; ok && slices.ContainsFunc(names, func(route string) bool {
				return strings.HasPrefix(name, "httproute/"+f.cp.namespace+"/"+route+"/")
			}) {
				configs[name] = value
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(dump)
	return configs, nil
}

// clusterHosts counts the endpoints of each Envoy cluster.
func (f *failover) clusterHosts(ctx context.Context) (map[string]int, error) {
	clusters, err := getJSON[struct {
		Statuses []struct {
			Name  string            `json:"name"`
			Hosts []json.RawMessage `json:"host_statuses"`
		} `json:"cluster_statuses"`
	}](ctx, f.admin+"/clusters?format=json")
	if err != nil {
		return nil, err
	}
	hosts := map[string]int{}
	for _, cluster := range clusters.Statuses {
		hosts[cluster.Name] = len(cluster.Hosts)
	}
	return hosts, nil
}

// retryStats reads the successful retries of each host's Envoy cluster.
func (f *failover) retryStats(t *testing.T, clusters map[string]string) map[string]int {
	t.Helper()
	stats, err := getJSON[struct {
		Stats []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"stats"`
	}](t.Context(), f.admin+"/stats?format=json")
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]int{}
	for host, cluster := range clusters {
		for _, stat := range stats.Stats {
			if stat.Name == "cluster."+cluster+"."+retrySuccessStat {
				value, err := strconv.Atoi(fmt.Sprint(stat.Value))
				if err != nil {
					t.Fatalf("stat %s: %v", stat.Name, err)
				}
				result[host] = value
			}
		}
	}
	return result
}

func routeCluster(t *testing.T, route map[string]any) string {
	t.Helper()
	var action struct {
		Route struct {
			Cluster string `json:"cluster"`
		} `json:"route"`
	}
	roundTrip(t, route, &action)
	return action.Route.Cluster
}

func retryPolicy(t *testing.T, route map[string]any) (map[string]any, bool) {
	t.Helper()
	var action struct {
		Route struct {
			RetryPolicy map[string]any `json:"retry_policy"`
		} `json:"route"`
	}
	roundTrip(t, route, &action)
	return action.Route.RetryPolicy, action.Route.RetryPolicy != nil
}

// filterMetadata is Envoy Gateway's metadata of a route, which the caller may change in place.
func filterMetadata(route map[string]any) map[string]any {
	if metadata, ok := route["metadata"].(map[string]any); ok {
		if filters, ok := metadata["filter_metadata"].(map[string]any); ok {
			if gateway, ok := filters["envoy-gateway"].(map[string]any); ok {
				return gateway
			}
		}
	}
	return map[string]any{}
}

// isAPIRoute reports whether Envoy Gateway generated the route from a rule named api.
func isAPIRoute(t *testing.T, route map[string]any) bool {
	t.Helper()
	var gateway struct {
		Resources []struct {
			SectionName string `json:"sectionName"`
		} `json:"resources"`
	}
	roundTrip(t, filterMetadata(route), &gateway)
	return slices.ContainsFunc(gateway.Resources, func(resource struct {
		SectionName string `json:"sectionName"`
	}) bool {
		return resource.SectionName == "api"
	})
}

func getJSON[T any](ctx context.Context, url string) (T, error) {
	var result T
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return result, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}

func roundTrip(t *testing.T, from, into any) {
	t.Helper()
	data, err := json.Marshal(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}
