package controller

import (
	"testing"

	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/team-loco/loco/controller/internal/envoygw"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// The assertions here encode the constraints established empirically in
// experiments/gateway-failover. Each is a property that, if violated, produces a config
// Envoy Gateway accepts and reports as healthy while doing the wrong thing at runtime.

func appWithFailover(enabled bool, peers ...locov1alpha1.FailoverPeer) *locov1alpha1.Application {
	app := &locov1alpha1.Application{}
	app.Spec.Region = "eu-west-1"
	app.Spec.ServiceSpec = &locov1alpha1.ServiceSpec{}
	if enabled || len(peers) > 0 {
		app.Spec.ServiceSpec.Failover = &locov1alpha1.FailoverSpec{Enabled: enabled, Peers: peers}
	}
	return app
}

func TestFailoverEnabled(t *testing.T) {
	peer := locov1alpha1.FailoverPeer{Region: "us-east-1", Gateway: "us-east-1.deploy-app.com"}

	cases := []struct {
		name string
		app  *locov1alpha1.Application
		want bool
	}{
		{"nil service spec", &locov1alpha1.Application{}, false},
		{"no failover block", appWithFailover(false), false},
		{"disabled with peers", appWithFailover(false, peer), false},
		{"enabled without peers", appWithFailover(true), false},
		{"enabled with peers", appWithFailover(true, peer), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := failoverEnabled(tc.app); got != tc.want {
				t.Fatalf("failoverEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// A Backend endpoint must be fqdn. With an ip endpoint Envoy Gateway emits two clusters
// behind a weighted route instead of one cluster with two priority levels, splitting
// traffic across regions permanently -- while still reporting the route as Accepted.
func TestBackendUsesFQDNEndpoint(t *testing.T) {
	b := envoygw.NewBackend("app-local", "ns")
	envoygw.SetFQDNBackendSpec(b, "app.ns.svc.cluster.local", 8080, false)

	spec, ok := b.Object["spec"].(map[string]any)
	if !ok {
		t.Fatal("spec is not a map")
	}
	endpoints, ok := spec["endpoints"].([]any)
	if !ok || len(endpoints) != 1 {
		t.Fatalf("want 1 endpoint, got %v", spec["endpoints"])
	}
	ep, ok := endpoints[0].(map[string]any)
	if !ok {
		t.Fatalf("endpoint is not a map: %v", endpoints[0])
	}
	if _, isIP := ep["ip"]; isIP {
		t.Fatal("endpoint uses ip; Envoy Gateway will silently degrade failover to a weighted split")
	}
	fqdn, ok := ep["fqdn"].(map[string]any)
	if !ok {
		t.Fatalf("endpoint is not fqdn: %v", ep)
	}
	if fqdn["hostname"] != "app.ns.svc.cluster.local" || fqdn["port"] != int64(8080) {
		t.Fatalf("unexpected fqdn endpoint: %v", fqdn)
	}
	if _, hasFallback := spec["fallback"]; hasFallback {
		t.Fatal("primary Backend must not set fallback")
	}
}

func TestPeerBackendIsMarkedFallback(t *testing.T) {
	b := envoygw.NewBackend("app-peers", "ns")
	envoygw.SetFQDNBackendSpec(b, "us-east-1.deploy-app.com", 443, true)

	spec, ok := b.Object["spec"].(map[string]any)
	if !ok {
		t.Fatal("spec is not a map")
	}
	if spec["fallback"] != true {
		t.Fatal("peer Backend must set fallback: true, or it becomes an equal-weight peer")
	}
}

// The loop guard must reference only the local Backend. If it carried the peer Backend
// too, a request forwarded by one region could be forwarded straight back.
func TestLoopGuardRuleHasNoPeerBackend(t *testing.T) {
	rule := loopGuardRule("app", nil)

	if len(rule.Matches) != 1 || len(rule.Matches[0].Headers) != 1 {
		t.Fatalf("want exactly one header match, got %+v", rule.Matches)
	}
	h := rule.Matches[0].Headers[0]
	if string(h.Name) != forwardedByHeader {
		t.Fatalf("loop guard matches %q, want %q", h.Name, forwardedByHeader)
	}
	if h.Type == nil || *h.Type != v1Gateway.HeaderMatchRegularExpression {
		t.Fatal("loop guard must match on presence via regex; Gateway API has no present-match type")
	}

	if len(rule.BackendRefs) != 1 {
		t.Fatalf("loop guard must have exactly one backendRef, got %d", len(rule.BackendRefs))
	}
	if got := string(rule.BackendRefs[0].Name); got != localBackendName("app") {
		t.Fatalf("loop guard targets %q, want the local backend %q", got, localBackendName("app"))
	}
}

// Order matters: local first (priority 0), peers second (priority 1).
func TestFailoverBackendRefsOrderAndKind(t *testing.T) {
	refs := failoverBackendRefs("app", nil)
	if len(refs) != 2 {
		t.Fatalf("want 2 backendRefs, got %d", len(refs))
	}
	if string(refs[0].Name) != localBackendName("app") {
		t.Fatalf("first backendRef is %q, want local", refs[0].Name)
	}
	if string(refs[1].Name) != peerBackendName("app") {
		t.Fatalf("second backendRef is %q, want peers", refs[1].Name)
	}
	for i, r := range refs {
		if r.Group == nil || string(*r.Group) != envoygw.Group {
			t.Fatalf("backendRef %d group = %v, want %s", i, r.Group, envoygw.Group)
		}
		if r.Kind == nil || string(*r.Kind) != "Backend" {
			t.Fatalf("backendRef %d kind = %v, want Backend; a Service ref will not group into priorities", i, r.Kind)
		}
	}
}

// The forwarded-by stamp must be a rule-level filter. Attached to a single backendRef it
// forces Envoy Gateway to split the cluster, because Envoy cannot mutate headers per
// priority level -- which silently destroys the failover.
func TestForwardedByFilterIsRuleLevel(t *testing.T) {
	filters := forwardedByFilter("eu-west-1")
	if len(filters) != 1 {
		t.Fatalf("want 1 filter, got %d", len(filters))
	}
	f := filters[0]
	if f.Type != v1Gateway.HTTPRouteFilterRequestHeaderModifier {
		t.Fatalf("filter type = %v", f.Type)
	}
	if f.RequestHeaderModifier == nil || len(f.RequestHeaderModifier.Set) != 1 {
		t.Fatal("filter must set exactly one header")
	}
	h := f.RequestHeaderModifier.Set[0]
	if string(h.Name) != forwardedByHeader || h.Value != "eu-west-1" {
		t.Fatalf("filter sets %s=%s, want %s=eu-west-1", h.Name, h.Value, forwardedByHeader)
	}

	refs := failoverBackendRefs("app", nil)
	for i, r := range refs {
		if len(r.Filters) != 0 {
			t.Fatalf("backendRef %d carries %d filters; the stamp must be rule-level", i, len(r.Filters))
		}
	}
}

func TestPeerPortDefaultsToTLS(t *testing.T) {
	if got := peerPort(locov1alpha1.FailoverPeer{}); got != locov1alpha1.DefaultFailoverPeerPort {
		t.Fatalf("default peer port = %d, want %d", got, locov1alpha1.DefaultFailoverPeerPort)
	}
	if got := peerPort(locov1alpha1.FailoverPeer{Port: 8443}); got != 8443 {
		t.Fatalf("explicit peer port = %d, want 8443", got)
	}
}

// Passive health checking is what promotes the peer; without it the fallback backend is
// configured but never used.
func TestBackendTrafficPolicyEnablesPassiveHealthAndRetry(t *testing.T) {
	p := envoygw.NewBackendTrafficPolicy("app-failover", "ns")
	envoygw.SetBackendTrafficPolicySpec(p, "app-route", envoygw.DefaultHealthCheck())

	spec, ok := p.Object["spec"].(map[string]any)
	if !ok {
		t.Fatal("spec is not a map")
	}

	targets, ok := spec["targetRefs"].([]any)
	if !ok {
		t.Fatal("targetRefs is not a list")
	}
	if len(targets) != 1 {
		t.Fatalf("want 1 targetRef, got %d", len(targets))
	}
	tr, ok := targets[0].(map[string]any)
	if !ok {
		t.Fatal("targetRef is not a map")
	}
	if tr["kind"] != "HTTPRoute" || tr["name"] != "app-route" {
		t.Fatalf("unexpected targetRef: %v", tr)
	}

	healthCheck, ok := spec["healthCheck"].(map[string]any)
	if !ok {
		t.Fatal("healthCheck is not a map")
	}
	passive, ok := healthCheck["passive"].(map[string]any)
	if !ok {
		t.Fatal("passive health check missing; the fallback would never be promoted")
	}
	// A Service with zero ready endpoints refuses connections rather than returning 5xx,
	// so this is the trigger that actually fires on a scaled-to-zero deployment.
	if passive["consecutiveLocalOriginFailures"] != int64(1) {
		t.Fatalf("consecutiveLocalOriginFailures = %v, want 1", passive["consecutiveLocalOriginFailures"])
	}
	if passive["baseEjectionTime"] != "5s" || passive["interval"] != "2s" {
		t.Fatalf("unexpected ejection timing: %v", passive)
	}

	retry, ok := spec["retry"].(map[string]any)
	if !ok {
		t.Fatal("retry missing; the first request after failure would surface a 503 to the client")
	}
	retryOn, ok := retry["retryOn"].(map[string]any)
	if !ok {
		t.Fatal("retryOn is not a map")
	}
	triggers, ok := retryOn["triggers"].([]any)
	if !ok {
		t.Fatal("triggers is not a list")
	}
	var hasConnectFailure bool
	for _, tr := range triggers {
		if tr == "connect-failure" {
			hasConnectFailure = true
		}
	}
	if !hasConnectFailure {
		t.Fatalf("retry triggers %v must include connect-failure", triggers)
	}
}
