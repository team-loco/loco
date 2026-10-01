package controller

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/team-loco/loco/controller/internal/envoygw"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// forwardedByHeader marks a request that a loco gateway has already forwarded once.
//
// It is set on every request leaving the primary route rule, not only on requests that
// actually fail over. Envoy cannot mutate headers per priority level within a cluster,
// so a per-backendRef filter would force Envoy Gateway to split the cluster in two and
// destroy the priority relationship the whole mechanism depends on. A locally served
// request therefore carries a header its app ignores; a request that crosses to a peer
// carries the signal the peer needs.
const forwardedByHeader = "x-loco-forwarded-by"

// failoverEnabled reports whether this Application should get cross-region failover
// wiring. Enabled with no peers is a valid, common state — a single-region deployment
// of a resource whose config allows failover — and must behave exactly like failover
// being off.
func failoverEnabled(locoRes *locov1alpha1.Application) bool {
	svc := locoRes.Spec.ServiceSpec
	if svc == nil || svc.Failover == nil {
		return false
	}
	return svc.Failover.Enabled && len(svc.Failover.Peers) > 0
}

func localBackendName(appName string) string  { return fmt.Sprintf("%s-local", appName) }
func peerBackendName(appName string) string   { return fmt.Sprintf("%s-peers", appName) }
func trafficPolicyName(appName string) string { return fmt.Sprintf("%s-failover", appName) }

func peerPort(p locov1alpha1.FailoverPeer) int32 {
	if p.Port > 0 {
		return p.Port
	}
	return locov1alpha1.DefaultFailoverPeerPort
}

// ensureFailover creates or removes the Envoy Gateway resources backing cross-region
// failover. It is a no-op that also cleans up when failover is disabled, so toggling the
// flag off converges rather than leaving orphans behind.
func (r *LocoResourceReconciler) ensureFailover(ctx context.Context, locoRes *locov1alpha1.Application) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)

	if !failoverEnabled(locoRes) {
		return r.removeFailoverResources(ctx, name, namespace)
	}

	svcPort := locoRes.Spec.ServiceSpec.Deployment.Port

	// Priority 0: the local Service, addressed by its cluster DNS name.
	//
	// Envoy resolves this to a single host (the Service VIP), not to individual pods, so
	// failover is all-or-nothing rather than proportional to pod health. Per-pod health
	// stays kube-proxy's concern. This is deliberate: partial spillover would send a
	// fraction of traffic across regions whenever a single pod was briefly unready.
	localFQDN := fmt.Sprintf("%s.%s.svc.cluster.local", name, namespace)
	local := envoygw.NewBackend(localBackendName(name), namespace)
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, local, func() error {
		local.SetLabels(map[string]string{"app": name})
		envoygw.SetFQDNBackendSpec(local, localFQDN, svcPort, false)
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensure local Backend: %w", err)
	}
	slog.InfoContext(ctx, "local failover Backend ensured", "name", localBackendName(name), "op", op)

	// Priority 1: every peer region's public gateway, in one Backend. Envoy load balances
	// within a priority level, so multiple peers share the spillover rather than needing
	// an ordering between them.
	peers := locoRes.Spec.ServiceSpec.Failover.Peers
	peerObj := envoygw.NewBackend(peerBackendName(name), namespace)
	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, peerObj, func() error {
		peerObj.SetLabels(map[string]string{"app": name})
		peerObj.Object["spec"] = peerBackendSpec(peers)
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensure peer Backend: %w", err)
	}
	slog.InfoContext(ctx, "peer failover Backend ensured", "name", peerBackendName(name), "peers", len(peers), "op", op)

	// Passive health checking is what actually triggers promotion of the peers.
	routeName := fmt.Sprintf("%s-route", name)
	policy := envoygw.NewBackendTrafficPolicy(trafficPolicyName(name), namespace)
	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
		policy.SetLabels(map[string]string{"app": name})
		envoygw.SetBackendTrafficPolicySpec(policy, routeName, envoygw.DefaultHealthCheck())
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensure BackendTrafficPolicy: %w", err)
	}
	slog.InfoContext(ctx, "failover BackendTrafficPolicy ensured", "name", trafficPolicyName(name), "op", op)

	return nil
}

// removeFailoverResources deletes the failover objects if they exist. Missing objects
// are not an error: this runs on every reconcile of every non-failover Application. A
// missing CRD is not an error either — a cluster that never uses failover has no reason
// to have Envoy Gateway's extension APIs installed.
func (r *LocoResourceReconciler) removeFailoverResources(ctx context.Context, name, namespace string) error {
	objs := []*unstructured.Unstructured{
		envoygw.NewBackend(localBackendName(name), namespace),
		envoygw.NewBackend(peerBackendName(name), namespace),
		envoygw.NewBackendTrafficPolicy(trafficPolicyName(name), namespace),
	}

	for _, obj := range objs {
		if err := r.Delete(ctx, obj); err != nil {
			if errors.IsNotFound(err) || meta.IsNoMatchError(err) {
				continue
			}
			return fmt.Errorf("delete %s %s: %w", obj.GetKind(), obj.GetName(), err)
		}
	}
	return nil
}

// peerBackendSpec builds the fallback Backend spec covering every peer region. All peers
// share priority 1, so Envoy load balances among them rather than needing an ordering.
func peerBackendSpec(peers []locov1alpha1.FailoverPeer) map[string]any {
	endpoints := make([]any, 0, len(peers))
	for _, p := range peers {
		endpoints = append(endpoints, map[string]any{
			"fqdn": map[string]any{
				"hostname": p.Gateway,
				"port":     int64(peerPort(p)),
			},
		})
	}
	return map[string]any{
		"fallback":  true,
		"endpoints": endpoints,
	}
}

// routeRules returns the HTTPRoute rules for an Application. Without failover that is a
// single rule to the local Service. With failover it is the loop guard followed by a
// primary rule spanning the local and peer Backends.
func routeRules(locoRes *locov1alpha1.Application, name string, backendPort *v1Gateway.PortNumber, primary v1Gateway.HTTPRouteRule) []v1Gateway.HTTPRouteRule {
	if !failoverEnabled(locoRes) {
		return []v1Gateway.HTTPRouteRule{primary}
	}
	primary.BackendRefs = failoverBackendRefs(name, backendPort)
	primary.Filters = forwardedByFilter(locoRes.Spec.Region)
	return []v1Gateway.HTTPRouteRule{
		loopGuardRule(name, backendPort),
		primary,
	}
}

// failoverBackendRefs returns the backendRefs for the primary route rule: the local
// Backend at priority 0 and the peer Backend at priority 1.
func failoverBackendRefs(name string, port *v1Gateway.PortNumber) []v1Gateway.HTTPBackendRef {
	group := v1Gateway.Group(envoygw.Group)
	kind := v1Gateway.Kind("Backend")
	return []v1Gateway.HTTPBackendRef{
		{
			BackendRef: v1Gateway.BackendRef{
				BackendObjectReference: v1Gateway.BackendObjectReference{
					Group: &group,
					Kind:  &kind,
					Name:  v1Gateway.ObjectName(localBackendName(name)),
					Port:  port,
				},
			},
		},
		{
			BackendRef: v1Gateway.BackendRef{
				BackendObjectReference: v1Gateway.BackendObjectReference{
					Group: &group,
					Kind:  &kind,
					Name:  v1Gateway.ObjectName(peerBackendName(name)),
					Port:  port,
				},
			},
		},
	}
}

// loopGuardRule matches requests that a peer gateway has already forwarded and pins them
// to the local backend. Without it, a failure affecting both regions has each gateway
// forwarding to the other until retry budgets are exhausted.
func loopGuardRule(name string, port *v1Gateway.PortNumber) v1Gateway.HTTPRouteRule {
	group := v1Gateway.Group(envoygw.Group)
	kind := v1Gateway.Kind("Backend")
	matchType := v1Gateway.HeaderMatchRegularExpression
	return v1Gateway.HTTPRouteRule{
		Matches: []v1Gateway.HTTPRouteMatch{
			{
				Headers: []v1Gateway.HTTPHeaderMatch{
					{
						Type:  &matchType,
						Name:  v1Gateway.HTTPHeaderName(forwardedByHeader),
						Value: ".+",
					},
				},
			},
		},
		BackendRefs: []v1Gateway.HTTPBackendRef{
			{
				BackendRef: v1Gateway.BackendRef{
					BackendObjectReference: v1Gateway.BackendObjectReference{
						Group: &group,
						Kind:  &kind,
						Name:  v1Gateway.ObjectName(localBackendName(name)),
						Port:  port,
					},
				},
			},
		},
	}
}

// forwardedByFilter stamps the current region onto every request leaving the primary
// rule. See forwardedByHeader for why this is rule-level rather than per-backendRef.
func forwardedByFilter(region string) []v1Gateway.HTTPRouteFilter {
	return []v1Gateway.HTTPRouteFilter{
		{
			Type: v1Gateway.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: &v1Gateway.HTTPHeaderFilter{
				Set: []v1Gateway.HTTPHeader{
					{Name: v1Gateway.HTTPHeaderName(forwardedByHeader), Value: region},
				},
			},
		},
	}
}
