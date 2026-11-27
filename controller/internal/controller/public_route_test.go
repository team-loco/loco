package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
					{
						Type:               string(gatewayv1.RouteConditionAccepted),
						Status:             metav1.ConditionTrue,
						ObservedGeneration: route.Generation,
					},
					{
						Type:               string(gatewayv1.RouteConditionResolvedRefs),
						Status:             metav1.ConditionTrue,
						ObservedGeneration: route.Generation,
					},
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

func TestPublicRouteIncludesAliases(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&gatewayv1.HTTPRoute{}).Build()
	app := testApplication()
	app.Spec.ServiceSpec.Routing = testRouting()
	app.Spec.ServiceSpec.Routing.Hostnames = []string{testHostName, "alias.example.com"}
	r := &ApplicationReconciler{
		Client:                   kube,
		APIReader:                kube,
		Gateway:                  testGateway,
		LocoNamespace:            testLocoNamespace,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}
	if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	routes := &gatewayv1.HTTPRouteList{}
	if err := kube.List(t.Context(), routes); err != nil {
		t.Fatal(err)
	}
	var hostnames []string
	for _, route := range routes.Items {
		for _, name := range route.Spec.Hostnames {
			hostnames = append(hostnames, string(name))
		}
	}
	slices.Sort(hostnames)
	want := slices.Clone(app.Spec.ServiceSpec.Routing.Hostnames)
	slices.Sort(want)
	if !slices.Equal(hostnames, want) {
		t.Fatalf("routed hostnames=%v want=%v", hostnames, want)
	}
	if !slices.Equal(app.Status.PublicRoute.Hostnames, app.Spec.ServiceSpec.Routing.Hostnames) {
		t.Fatalf("reported hostnames = %v", app.Status.PublicRoute.Hostnames)
	}
}

func TestPublicRouteRequiresEveryHostnameAndPrunesAliases(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&gatewayv1.HTTPRoute{}).Build()
	app := testApplication()
	app.Spec.ServiceSpec.Routing = testRouting()
	for i := range 1 {
		app.Spec.ServiceSpec.Routing.Hostnames = append(
			app.Spec.ServiceSpec.Routing.Hostnames,
			fmt.Sprintf("alias%d.example.com", i),
		)
	}
	r := &ApplicationReconciler{
		Client:                   kube,
		APIReader:                kube,
		Gateway:                  testGateway,
		LocoNamespace:            testLocoNamespace,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}
	if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	routes := &gatewayv1.HTTPRouteList{}
	if err := kube.List(t.Context(), routes); err != nil {
		t.Fatal(err)
	}
	if len(routes.Items) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes.Items))
	}

	var routed []string
	for _, route := range routes.Items {
		for _, hostname := range route.Spec.Hostnames {
			routed = append(routed, string(hostname))
		}
	}
	slices.Sort(routed)
	want := slices.Clone(app.Spec.ServiceSpec.Routing.Hostnames)
	slices.Sort(want)
	if !slices.Equal(routed, want) {
		t.Fatalf("routed hostnames = %v, want %v", routed, want)
	}
	for i := range routes.Items {
		route := &routes.Items[i]
		if err := kube.Get(t.Context(), client.ObjectKeyFromObject(route), route); err != nil {
			t.Fatal(err)
		}
		if len(route.Spec.Hostnames) != 1 {
			t.Fatalf("too many route hostnames: %d", len(route.Spec.Hostnames))
		}
		route.Status.Parents = []gatewayv1.RouteParentStatus{
			{
				ParentRef:      route.Spec.ParentRefs[0],
				ControllerName: envoyGatewayController,
				Conditions: []metav1.Condition{
					{
						Type:               string(gatewayv1.RouteConditionAccepted),
						Status:             metav1.ConditionTrue,
						ObservedGeneration: route.Generation,
					},
					{
						Type:               string(gatewayv1.RouteConditionResolvedRefs),
						Status:             metav1.ConditionTrue,
						ObservedGeneration: route.Generation,
					},
				},
			},
		}
		if err := kube.Status().Update(t.Context(), route); err != nil {
			t.Fatal(err)
		}
		if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
			t.Fatal(err)
		}
		if app.Status.PublicRoute.Ready != (i == 1) {
			t.Fatalf("ready after %d accepted routes = %v", i+1, app.Status.PublicRoute.Ready)
		}
	}
	app.Spec.ServiceSpec.Routing.Hostnames = []string{testHostName}
	if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	if err := kube.List(t.Context(), routes); err != nil {
		t.Fatal(err)
	}
	if len(routes.Items) != 1 {
		t.Fatalf("routes after removing aliases = %d", len(routes.Items))
	}

	app.Spec.ServiceSpec.Routing = nil
	if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	if err := kube.List(t.Context(), routes); err != nil {
		t.Fatal(err)
	}
	if len(routes.Items) != 0 || app.Status.PublicRoute != nil {
		t.Fatalf("private app retains routes or observation: %d %+v", len(routes.Items), app.Status.PublicRoute)
	}
}

func TestPublicRouteErrorNeverReportsReady(t *testing.T) {
	for _, operation := range []string{"apply", "read", "prune"} {
		t.Run(operation, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := gatewayv1.Install(scheme); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected Kubernetes failure")
			funcs := interceptor.Funcs{}
			switch operation {
			case "apply":
				funcs.Apply = func(context.Context, client.WithWatch, runtime.ApplyConfiguration, ...client.ApplyOption) error {
					return injected
				}
			case "read":
				funcs.Get = func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					return injected
				}
			case "prune":
				funcs.List = func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
					return injected
				}
			}
			kube := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&gatewayv1.HTTPRoute{}).
				Build()
			app := testApplication()
			app.Spec.ServiceSpec.Routing = testRouting()
			r := &ApplicationReconciler{
				Client:                   kube,
				APIReader:                kube,
				Gateway:                  testGateway,
				LocoNamespace:            testLocoNamespace,
				WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			}

			if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
				t.Fatal(err)
			}
			route := &gatewayv1.HTTPRoute{}
			if err := kube.Get(
				t.Context(),
				client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: getRouteName(app)},
				route,
			); err != nil {
				t.Fatal(err)
			}
			route.Status.Parents = []gatewayv1.RouteParentStatus{
				{
					ParentRef:      route.Spec.ParentRefs[0],
					ControllerName: envoyGatewayController,
					Conditions: []metav1.Condition{
						{
							Type:               string(gatewayv1.RouteConditionAccepted),
							Status:             metav1.ConditionTrue,
							ObservedGeneration: route.Generation,
						},
						{
							Type:               string(gatewayv1.RouteConditionResolvedRefs),
							Status:             metav1.ConditionTrue,
							ObservedGeneration: route.Generation,
						},
					},
				},
			}
			if err := kube.Status().Update(t.Context(), route); err != nil {
				t.Fatal(err)
			}
			if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
				t.Fatal(err)
			}
			if !app.Status.PublicRoute.Ready {
				t.Fatal("initial route not ready")
			}
			failing := interceptor.NewClient(kube, funcs)
			r.Client = failing
			r.APIReader = failing
			if err := r.ensureHTTPRoute(t.Context(), app); !errors.Is(err, injected) {
				t.Fatalf("error = %v", err)
			}
			if app.Status.PublicRoute == nil || app.Status.PublicRoute.Ready {
				t.Fatalf("error observation = %+v", app.Status.PublicRoute)
			}
		})
	}
}

func TestPublicRouteRejectsPartialHostnameAcceptance(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&gatewayv1.HTTPRoute{}).Build()
	app := testApplication()
	app.Spec.ServiceSpec.Routing = testRouting()
	app.Spec.ServiceSpec.Routing.Hostnames = []string{testHostName, "other.unserved.example.net"}
	r := &ApplicationReconciler{
		Client:                   kube,
		APIReader:                kube,
		Gateway:                  testGateway,
		LocoNamespace:            testLocoNamespace,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}
	if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	routes := &gatewayv1.HTTPRouteList{}
	if err := kube.List(t.Context(), routes); err != nil {
		t.Fatal(err)
	}
	for _, route := range routes.Items {
		// Gateway API accepts a batch if any hostname intersects an eligible listener.
		accepted := metav1.ConditionFalse
		if slices.Contains(route.Spec.Hostnames, gatewayv1.Hostname(testHostName)) {
			accepted = metav1.ConditionTrue
		}
		route.Status.Parents = []gatewayv1.RouteParentStatus{
			{
				ParentRef:      route.Spec.ParentRefs[0],
				ControllerName: envoyGatewayController,
				Conditions: []metav1.Condition{
					{
						Type:               string(gatewayv1.RouteConditionAccepted),
						Status:             accepted,
						ObservedGeneration: route.Generation,
					},
					{
						Type:               string(gatewayv1.RouteConditionResolvedRefs),
						Status:             metav1.ConditionTrue,
						ObservedGeneration: route.Generation,
					},
				},
			},
		}
		if err := kube.Status().Update(t.Context(), &route); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	if app.Status.PublicRoute.Ready {
		t.Fatal("one covered hostname advertised an uncovered alias as ready")
	}
}
