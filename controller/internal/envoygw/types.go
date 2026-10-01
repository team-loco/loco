// Package envoygw builds the two Envoy Gateway extension resources the failover
// path needs: Backend and BackendTrafficPolicy.
//
// These are declared locally as unstructured objects rather than by importing
// github.com/envoyproxy/gateway, which publishes no standalone api module — pulling
// it in would add the whole Envoy Gateway project as a dependency for two struct
// definitions.
package envoygw

import (
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// Group is the Envoy Gateway extension API group.
	Group = "gateway.envoyproxy.io"
	// Version is the extension API version.
	Version = "v1alpha1"

	kindBackend              = "Backend"
	kindBackendTrafficPolicy = "BackendTrafficPolicy"
)

// BackendGVK identifies an Envoy Gateway Backend.
func BackendGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: Group, Version: Version, Kind: kindBackend}
}

// BackendTrafficPolicyGVK identifies an Envoy Gateway BackendTrafficPolicy.
func BackendTrafficPolicyGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: Group, Version: Version, Kind: kindBackendTrafficPolicy}
}

// NewBackend returns an empty Backend object with only its GVK and name set, for use
// as the target of a CreateOrUpdate.
func NewBackend(name, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(BackendGVK())
	u.SetName(name)
	u.SetNamespace(namespace)
	return u
}

// NewBackendTrafficPolicy returns an empty BackendTrafficPolicy with only its GVK and
// name set.
func NewBackendTrafficPolicy(name, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(BackendTrafficPolicyGVK())
	u.SetName(name)
	u.SetNamespace(namespace)
	return u
}

// SetFQDNBackendSpec writes a Backend spec with a single fqdn endpoint.
//
// fqdn is required, not merely preferred: Envoy Gateway only groups multiple
// backendRefs into one Envoy cluster with priority levels when every referenced
// Backend uses an fqdn endpoint. With an ip endpoint it emits separate clusters behind
// a weighted route instead, splitting traffic across regions at all times — with no
// warning and with the route still reporting Accepted.
func SetFQDNBackendSpec(u *unstructured.Unstructured, hostname string, port int32, fallback bool) {
	spec := map[string]any{
		"endpoints": []any{
			map[string]any{
				"fqdn": map[string]any{
					"hostname": hostname,
					"port":     int64(port),
				},
			},
		},
	}
	if fallback {
		spec["fallback"] = true
	}
	u.Object["spec"] = spec
}

// HealthCheckSettings tunes how quickly an unhealthy local backend is ejected, which is
// what promotes the fallback peer.
type HealthCheckSettings struct {
	BaseEjectionTimeSeconds int32
	IntervalSeconds         int32
	Consecutive5xxErrors    int32
	ConsecutiveLocalFailure int32
	NumRetries              int32
}

// DefaultHealthCheck returns settings tuned for fast regional failover.
func DefaultHealthCheck() HealthCheckSettings {
	return HealthCheckSettings{
		BaseEjectionTimeSeconds: 5,
		IntervalSeconds:         2,
		Consecutive5xxErrors:    1,
		ConsecutiveLocalFailure: 1,
		NumRetries:              2,
	}
}

// SetBackendTrafficPolicySpec writes a spec targeting an HTTPRoute with passive health
// checking and retries.
//
// Passive health checking is what makes failover happen at all: it ejects the local
// host so priority-0 health drops and Envoy promotes the priority-1 peer. The retry
// policy absorbs the first failure — the one that arrives before ejection has taken
// effect — so clients see a slightly slow success rather than a 503.
func SetBackendTrafficPolicySpec(u *unstructured.Unstructured, routeName string, hc HealthCheckSettings) {
	u.Object["spec"] = map[string]any{
		"targetRefs": []any{
			map[string]any{
				"group": "gateway.networking.k8s.io",
				"kind":  "HTTPRoute",
				"name":  routeName,
			},
		},
		"healthCheck": map[string]any{
			"passive": map[string]any{
				"baseEjectionTime":               durationSeconds(hc.BaseEjectionTimeSeconds),
				"interval":                       durationSeconds(hc.IntervalSeconds),
				"maxEjectionPercent":             int64(100),
				"consecutive5XxErrors":           int64(hc.Consecutive5xxErrors),
				"consecutiveLocalOriginFailures": int64(hc.ConsecutiveLocalFailure),
			},
		},
		"retry": map[string]any{
			"numRetries": int64(hc.NumRetries),
			"retryOn": map[string]any{
				"triggers": []any{"connect-failure", "reset", "5xx"},
			},
		},
	}
}

func durationSeconds(s int32) string {
	return strconv.Itoa(int(s)) + "s"
}
