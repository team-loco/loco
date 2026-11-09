package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	routeManagerWait = 5 * time.Second
	routeManagerPoll = 50 * time.Millisecond
	disabledListener = "0"
)

var _ = Describe("Application manager HTTPRoutes", func() {
	It("recreates a deleted route and corrects route drift", func() {
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 k8sClient.Scheme(),
			Cache:                  CacheOptions(testNamespace, "", false),
			Metrics:                metricsserver.Options{BindAddress: disabledListener},
			HealthProbeBindAddress: disabledListener,
		})
		Expect(err).NotTo(HaveOccurred())
		r := &ApplicationReconciler{
			Client:                   mgr.GetClient(),
			APIReader:                mgr.GetAPIReader(),
			Scheme:                   mgr.GetScheme(),
			LocoNamespace:            testNamespace,
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
		}
		Expect(r.SetupWithManager(mgr)).To(Succeed())

		app := isolationTestApplication("ws-route-watch", "route-watch")
		namespace := testWorkspaceNamespace(app)
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(ensureTestNamespace(ctx, k8sClient, app)).To(Succeed())
		seed := *r
		seed.Client, seed.APIReader = k8sClient, k8sClient
		// Seed the watched Services before the manager starts so their create events cannot repair a route.
		Expect(seed.ensureService(ctx, app)).To(Succeed())
		Expect(seed.ensurePrivateService(ctx, app)).To(Succeed())
		_, err = seed.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		depKey := client.ObjectKey{Namespace: namespace, Name: testAppName(app)}
		dep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		// Start with ready replicas so no scheduled rollout reconciliation can repair the route.
		replicas := app.Spec.ServiceSpec.Resources.Replicas.Min
		dep.Status = appsv1.DeploymentStatus{
			ObservedGeneration: dep.Generation,
			Replicas:           replicas,
			UpdatedReplicas:    replicas,
			ReadyReplicas:      replicas,
			AvailableReplicas:  replicas,
		}
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())

		managerCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- mgr.Start(managerCtx) }()
		DeferCleanup(func() {
			stop()
			Eventually(done, routeManagerWait).Should(Receive(BeNil()))
		})

		appKey := client.ObjectKeyFromObject(app)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
			g.Expect(app.Status.Phase).To(Equal(locov1alpha1.ApplicationPhaseReady))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		appVersion, depVersion := app.ResourceVersion, dep.ResourceVersion
		route := &v1Gateway.HTTPRoute{}
		key := client.ObjectKey{Namespace: namespace, Name: getRouteName(app)}
		Expect(k8sClient.Get(ctx, key, route)).To(Succeed())
		originalUID := route.UID
		hostname := v1Gateway.Hostname(app.Spec.ServiceSpec.Routing.HostName)
		Expect(route.Spec.Hostnames).To(ConsistOf(hostname))

		Expect(k8sClient.Delete(ctx, route)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, route)).To(Succeed())
			g.Expect(route.UID).NotTo(Equal(originalUID))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())

		route.Spec.Hostnames = []v1Gateway.Hostname{"drift.example.com"}
		Expect(k8sClient.Update(ctx, route)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, route)).To(Succeed())
			g.Expect(route.Spec.Hostnames).To(ConsistOf(hostname))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())

		// Even matching owner annotations do not put another controller's routes in our cache.
		unrelated := &v1Gateway.HTTPRoute{
			Name:        "unrelated",
			Namespace:   namespace,
			Annotations: ownerAnnotations(app),
			Spec:        *route.Spec.DeepCopy(),
		}
		Expect(k8sClient.Create(ctx, unrelated)).To(Succeed())
		Eventually(func(g Gomega) {
			cached := &v1Gateway.HTTPRouteList{}
			g.Expect(mgr.GetClient().List(ctx, cached, client.InNamespace(namespace))).To(Succeed())
			g.Expect(cached.Items).To(HaveLen(1))
			g.Expect(cached.Items[0].Name).To(Equal(route.Name))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		Expect(app.ResourceVersion).To(Equal(appVersion))
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.ResourceVersion).To(Equal(depVersion))

		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, appKey, app))
		}, routeManagerWait, routeManagerPoll).Should(BeTrue())
	})
})
