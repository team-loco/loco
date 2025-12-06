package kube

import (
	"context"
	"fmt"
	"log/slog"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"
)

// CreateHTTPRoute creates an HTTPRoute for the deployment via the Loco gateway
func (kc *Client) CreateHTTPRoute(ctx context.Context, ldc *LocoDeploymentContext) (*v1Gateway.HTTPRoute, error) {
	slog.InfoContext(ctx, "Creating HTTPRoute", "namespace", ldc.Namespace(), "name", ldc.HTTPRouteName())

	hostname := ""
	pathType := v1Gateway.PathMatchPathPrefix
	timeout := DefaultRequestTimeout

	route := &v1Gateway.HTTPRoute{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      ldc.HTTPRouteName(),
			Namespace: ldc.Namespace(),
			Labels:    ldc.Labels(),
		},
		Spec: v1Gateway.HTTPRouteSpec{
			CommonRouteSpec: v1Gateway.CommonRouteSpec{
				ParentRefs: []v1Gateway.ParentReference{
					{
						Name:      v1Gateway.ObjectName(LocoGatewayName),
						Namespace: ptrToNamespace(LocoNS),
					},
				},
			},
			Hostnames: []v1Gateway.Hostname{v1Gateway.Hostname(hostname)},
			Rules: []v1Gateway.HTTPRouteRule{
				{
					Matches: []v1Gateway.HTTPRouteMatch{
						{
							Path: &v1Gateway.HTTPPathMatch{
								Type:  &pathType,
								Value: ptrToString(ldc.Config.Routing.PathPrefix),
							},
						},
					},
					Timeouts: &v1Gateway.HTTPRouteTimeouts{
						Request: ptrToDuration(timeout),
					},
					BackendRefs: []v1Gateway.HTTPBackendRef{
						{
							BackendRef: v1Gateway.BackendRef{
								BackendObjectReference: v1Gateway.BackendObjectReference{
									Name: v1Gateway.ObjectName(ldc.ServiceName()),
									Port: ptrToPortNumber(int(DefaultServicePort)),
									Kind: ptrToKind("Service"),
								},
							},
						},
					},
				},
			},
		},
	}

	createdRoute, err := kc.GatewaySet.GatewayV1().HTTPRoutes(ldc.Namespace()).Create(ctx, route, metaV1.CreateOptions{})
	if err != nil {
		slog.ErrorContext(ctx, "Failed to create HTTPRoute", "name", ldc.HTTPRouteName(), "error", err)
		return nil, fmt.Errorf("failed to create HTTPRoute: %w", err)
	}

	slog.InfoContext(ctx, "HTTPRoute created", "name", ldc.HTTPRouteName(), "hostname", hostname)
	return createdRoute, nil
}

// Helper functions for gateway API pointer conversions
func ptrToPortNumber(p int) *v1Gateway.PortNumber {
	n := v1Gateway.PortNumber(p)
	return &n
}

func ptrToNamespace(n string) *v1Gateway.Namespace {
	ns := v1Gateway.Namespace(n)
	return &ns
}

func ptrToDuration(d string) *v1Gateway.Duration {
	t := v1Gateway.Duration(d)
	return &t
}

func ptrToKind(k string) *v1Gateway.Kind {
	t := v1Gateway.Kind(k)
	return &t
}
