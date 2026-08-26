package controller

import (
	"os"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/team-loco/loco/controller/internal/envoygw"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// TestEmitManifests writes the resources the controller generates for a failover-enabled
// Application to testdata/generated-failover.yaml, using the same builder functions the
// reconciler calls.
//
// It is a golden-file generator rather than an assertion. The output is applied to a real
// two-cluster kind setup (experiments/gateway-failover) to confirm the controller's own
// output makes Envoy build priority levels rather than a weighted split -- the failure
// mode that Envoy Gateway accepts silently.
//
//	EMIT_MANIFESTS=1 go test ./internal/controller -run TestEmitManifests
func TestEmitManifests(t *testing.T) {
	if os.Getenv("EMIT_MANIFESTS") == "" {
		t.Skip("set EMIT_MANIFESTS=1 to regenerate testdata/generated-failover.yaml")
	}

	const (
		name = "demo-app"
		ns   = "default"
		port = int32(80)
	)

	app := &locov1alpha1.Application{}
	app.Spec.Region = "eu-west-1"
	app.Spec.ServiceSpec = &locov1alpha1.ServiceSpec{
		Failover: &locov1alpha1.FailoverSpec{
			Enabled: true,
			Peers: []locov1alpha1.FailoverPeer{
				{Region: "us-east-1", Gateway: "us-east-1.deploy-app.com", Port: 443},
			},
		},
	}

	local := envoygw.NewBackend(localBackendName(name), ns)
	local.SetLabels(map[string]string{"app": name})
	envoygw.SetFQDNBackendSpec(local, name+"."+ns+".svc.cluster.local", port, false)

	peersObj := envoygw.NewBackend(peerBackendName(name), ns)
	peersObj.SetLabels(map[string]string{"app": name})
	peersObj.Object["spec"] = peerBackendSpec(app.Spec.ServiceSpec.Failover.Peers)

	policy := envoygw.NewBackendTrafficPolicy(trafficPolicyName(name), ns)
	policy.SetLabels(map[string]string{"app": name})
	envoygw.SetBackendTrafficPolicySpec(policy, name+"-route", envoygw.DefaultHealthCheck())

	pathType := v1Gateway.PathMatchPathPrefix
	pathValue := "/"
	backendPort := ptrToPortNumber(int(port))
	gwNS := v1Gateway.Namespace(ns)

	primary := v1Gateway.HTTPRouteRule{
		Matches: []v1Gateway.HTTPRouteMatch{
			{Path: &v1Gateway.HTTPPathMatch{Type: &pathType, Value: &pathValue}},
		},
		BackendRefs: []v1Gateway.HTTPBackendRef{
			{BackendRef: v1Gateway.BackendRef{BackendObjectReference: v1Gateway.BackendObjectReference{
				Name: v1Gateway.ObjectName(name), Port: backendPort, Kind: ptrToKind("Service"),
			}}},
		},
	}

	route := &v1Gateway.HTTPRoute{
		TypeMeta: metav1.TypeMeta{APIVersion: "gateway.networking.k8s.io/v1", Kind: "HTTPRoute"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name + "-route", Namespace: ns, Labels: map[string]string{"app": name},
		},
		Spec: v1Gateway.HTTPRouteSpec{
			Hostnames: []v1Gateway.Hostname{"app.demo.local"},
			CommonRouteSpec: v1Gateway.CommonRouteSpec{
				ParentRefs: []v1Gateway.ParentReference{{Name: "eg", Namespace: &gwNS}},
			},
			Rules: routeRules(app, name, backendPort, primary),
		},
	}

	var out []byte
	for i, doc := range []any{local.Object, peersObj.Object, route, policy.Object} {
		b, err := yaml.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal doc %d: %v", i, err)
		}
		if i > 0 {
			out = append(out, []byte("---\n")...)
		}
		out = append(out, b...)
	}

	if err := os.WriteFile("testdata/generated-failover.yaml", out, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote testdata/generated-failover.yaml (%d bytes)", len(out))
}
