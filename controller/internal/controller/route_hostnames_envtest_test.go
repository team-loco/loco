package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Regional route hostnames", func() {
	routeFor := func(name, hostname string) *v1Gateway.HTTPRoute {
		app := workspaceApp(name, name)
		app.Spec.Region = testRegion
		// Envtest never finishes deleting a namespace, so each spec needs its own.
		app.Spec.EnvironmentID = name
		app.Spec.ServiceSpec.Routing = &locov1alpha1.RoutingSpec{HostName: hostname, PathPrefix: "/", IdleTimeout: 60}
		namespace := &corev1.Namespace{Name: testEnvironmentNamespace(app)}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client: k8sClient, LocoNamespace: testLocoNamespace, Gateway: testGateway,
			AppDomains: []string{"onloco.app", "onloco.build"}, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		}
		Expect(r.ensureHTTPRoute(ctx, app)).To(Succeed())
		route := &v1Gateway.HTTPRoute{}
		key := client.ObjectKey{Namespace: namespace.Name, Name: getRouteName(app)}
		Expect(k8sClient.Get(ctx, key, route)).To(Succeed())
		return route
	}

	It("also routes an app domain label under the application's region", func() {
		route := routeFor("regional", "storefront.onloco.build")
		Expect(route.Spec.Hostnames).To(Equal([]v1Gateway.Hostname{
			"storefront.onloco.build", "storefront." + testRegion + ".onloco.build",
		}))
	})

	It("routes a hostname outside the app domains only as itself", func() {
		route := routeFor("custom", "shop.example.com")
		Expect(route.Spec.Hostnames).To(Equal([]v1Gateway.Hostname{"shop.example.com"}))
	})
})
