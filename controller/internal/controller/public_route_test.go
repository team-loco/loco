package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestPublicRouteRequiresCurrentGatewayAcceptance(t *testing.T) {
	const unrelatedGateway = "unrelated-gateway"
	for _, tc := range []struct {
		name   string
		mutate func(*gatewayv1.HTTPRoute)
		ready  bool
	}{
		{"accepted", func(*gatewayv1.HTTPRoute) {}, true},
		{"missing status", func(r *gatewayv1.HTTPRoute) { r.Status.Parents = nil }, false},
		{"rejected", func(r *gatewayv1.HTTPRoute) {
			r.Status.Parents[0].Conditions[0].Status = metav1.ConditionFalse
		}, false},
		{"unresolved backend", func(r *gatewayv1.HTTPRoute) {
			r.Status.Parents[0].Conditions[1].Status = metav1.ConditionFalse
		}, false},
		{"stale acceptance", func(r *gatewayv1.HTTPRoute) { r.Status.Parents[0].Conditions[0].ObservedGeneration-- }, false},
		{"stale resolution", func(r *gatewayv1.HTTPRoute) { r.Status.Parents[0].Conditions[1].ObservedGeneration-- }, false},
		{"different gateway", func(r *gatewayv1.HTTPRoute) {
			r.Status.Parents[0].ParentRef.Name = unrelatedGateway
		}, false},
		{"different namespace", func(r *gatewayv1.HTTPRoute) {
			r.Status.Parents[0].ParentRef.Namespace = ptr.To(gatewayv1.Namespace(unrelatedGateway))
		}, false},
		{"different controller", func(r *gatewayv1.HTTPRoute) {
			r.Status.Parents[0].ControllerName = "other.example/controller"
		}, false},
		{"missing acceptance", func(r *gatewayv1.HTTPRoute) {
			r.Status.Parents[0].Conditions = r.Status.Parents[0].Conditions[1:]
		}, false},
		{"missing resolution", func(r *gatewayv1.HTTPRoute) {
			r.Status.Parents[0].Conditions = r.Status.Parents[0].Conditions[:1]
		}, false},
		{"unknown acceptance", func(r *gatewayv1.HTTPRoute) {
			r.Status.Parents[0].Conditions[0].Status = metav1.ConditionUnknown
		}, false},
		{"split conditions", func(r *gatewayv1.HTTPRoute) {
			other := r.Status.Parents[0].DeepCopy()
			other.ParentRef.Name = unrelatedGateway
			other.Conditions = other.Conditions[1:]
			r.Status.Parents[0].Conditions = r.Status.Parents[0].Conditions[:1]
			r.Status.Parents = append(r.Status.Parents, *other)
		}, false},
		{"unrelated parent first", func(r *gatewayv1.HTTPRoute) {
			other := r.Status.Parents[0].DeepCopy()
			other.ParentRef.Name = unrelatedGateway
			r.Status.Parents = append([]gatewayv1.RouteParentStatus{*other}, r.Status.Parents...)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := gatewayv1.Install(scheme); err != nil {
				t.Fatal(err)
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&gatewayv1.HTTPRoute{}).Build()
			app := testApplication()
			app.Spec.ServiceSpec.Routing = testRouting()
			app.Annotations = map[string]string{loco.KeyPlacementRevision: "7"}
			r := &ApplicationReconciler{
				Client: kube, APIReader: kube, Gateway: testGateway, LocoNamespace: testLocoNamespace,
				WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			}
			testCtx := context.Background()
			if err := r.ensureHTTPRoute(testCtx, app); err != nil {
				t.Fatal(err)
			}
			route := &gatewayv1.HTTPRoute{}
			key := client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: getRouteName(app)}
			if err := kube.Get(testCtx, key, route); err != nil {
				t.Fatal(err)
			}
			route.Generation = 4
			if err := kube.Update(testCtx, route); err != nil {
				t.Fatal(err)
			}
			route.Status.Parents = []gatewayv1.RouteParentStatus{{
				ParentRef:      route.Spec.ParentRefs[0],
				ControllerName: "gateway.envoyproxy.io/gatewayclass-controller",
				Conditions: []metav1.Condition{
					{Type: "Accepted", Status: metav1.ConditionTrue, ObservedGeneration: route.Generation},
					{Type: "ResolvedRefs", Status: metav1.ConditionTrue, ObservedGeneration: route.Generation},
				},
			}}
			tc.mutate(route)
			if err := kube.Status().Update(testCtx, route); err != nil {
				t.Fatal(err)
			}
			if err := r.ensureHTTPRoute(testCtx, app); err != nil {
				t.Fatal(err)
			}
			got := app.Status.PublicRoute
			if got == nil {
				t.Fatal("public route observation missing")
			}
			if got.Ready != tc.ready || got.Revision != 7 {
				t.Fatalf("observation = %+v, want ready=%v revision=7", got, tc.ready)
			}
			if len(got.Hostnames) != 1 || got.Hostnames[0] != testHostName {
				t.Fatalf("hostnames = %v", got.Hostnames)
			}
		})
	}
}
