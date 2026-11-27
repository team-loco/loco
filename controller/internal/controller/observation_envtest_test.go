package controller

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Time-driven rollout transitions", func() {
	It("proves a serving revision without waiting for another Pod event", func() {
		app := deadlineApp("quiet-proof", 1)
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		seed := &ApplicationReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: k8sClient.Scheme(),
			LocoNamespace: testNamespace, Gateway: testGateway, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry: testTelemetry(), HealthyAfter: 8 * time.Second}
		reconcileStuckApp(seed, app)
		pod := serveReplica(app, "quiet-proof-pod")
		setDeploymentCounts(app, 1, 1)
		reconcileStuckApp(seed, app)
		Expect(app.Status.Rollout.ProvenRevision).To(BeZero())
		Expect(app.Status.Rollout.Available).To(BeTrue())
		originalPodVersion := pod.ResourceVersion
		recorder := startObservationManager(seed)
		Eventually(
			func() error { return environmentControllersIdle(recorder) },
			routeManagerWait,
			routeManagerPoll,
		).Should(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
		Expect(app.Status.Rollout.ProvenRevision).To(BeZero())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
			g.Expect(app.Status.Rollout.ProvenRevision).To(Equal(int64(2)))
			g.Expect(app.Status.Rollout.Healthy).To(BeTrue())
		}, 12*time.Second, routeManagerPoll).Should(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
		Expect(pod.ResourceVersion).To(Equal(originalPodVersion), "Pod updates must not mask a missing timer")
		fmt.Fprintln(
			GinkgoWriter,
			"synthetic unchanged serving Pod: revision became proven and healthy at its qualification deadline",
		)
		result, err := seed.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero(), "completed transition must leave no periodic rollout polling")
		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
	})

	It("stops an unschedulable revision at its recorded deadline without another Pod event", func() {
		app := deadlineApp("quiet-timeout", 1)
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		seed := &ApplicationReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: k8sClient.Scheme(),
			LocoNamespace: testNamespace, Gateway: testGateway, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry: testTelemetry(), HealthyAfter: testHealthyAfter}
		reconcileStuckApp(seed, app)
		pod := createReplica(app, "quiet-timeout-pod")
		setUnschedulable(pod)
		reconcileStuckApp(seed, app)
		deadline := time.Now().Truncate(time.Second).Add(8 * time.Second)
		app.Status.Rollout.Deadline.At = metav1.NewTime(deadline)
		Expect(k8sClient.Status().Update(ctx, app)).To(Succeed())
		originalPodVersion := pod.ResourceVersion
		recorder := startObservationManager(seed)
		Eventually(
			func() error { return environmentControllersIdle(recorder) },
			routeManagerWait,
			routeManagerPoll,
		).Should(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
		Expect(app.Status.Rollout.Stopped).To(BeNil())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(app), app)).To(Succeed())
			g.Expect(app.Status.Rollout.Stopped).NotTo(BeNil())
			g.Expect(app.Status.Rollout.Stopped.Reason).To(Equal(loco.ProblemDeployTimeout))
		}, 12*time.Second, routeManagerPoll).Should(Succeed())
		Expect(app.Status.Rollout.Stopped.At.Time).NotTo(BeTemporally("<", deadline))
		deployment := appDeployment(app)
		Expect(deployment.Spec.Paused).To(BeTrue())
		Expect(*deployment.Spec.Replicas).To(BeZero())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
		Expect(pod.ResourceVersion).To(Equal(originalPodVersion))
		fmt.Fprintf(
			GinkgoWriter,
			"synthetic unchanged unschedulable Pod: reason=%s deployment paused with zero replicas; deadline lateness=%s\n",
			app.Status.Rollout.Stopped.Reason,
			time.Since(deadline),
		)
		result, err := seed.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero(), "completed transition must leave no periodic rollout polling")
		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
	})
})

func startObservationManager(seed *ApplicationReconciler) *tracetest.SpanRecorder {
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	DeferCleanup(func() { otel.SetTracerProvider(previous); Expect(provider.Shutdown(ctx)).To(Succeed()) })
	GinkgoHelper()
	mgr, err := ctrl.NewManager(
		cfg,
		ctrl.Options{
			Scheme: k8sClient.Scheme(),
			Cache:  CacheOptions(testNamespace, "", false),
			Controller: config.Controller{
				SkipNameValidation: new(true),
			},
			Metrics:                metricsserver.Options{BindAddress: disabledListener},
			HealthProbeBindAddress: disabledListener,
		},
	)
	Expect(err).NotTo(HaveOccurred())
	r := ApplicationReconciler{
		Client:                   mgr.GetClient(),
		APIReader:                mgr.GetAPIReader(),
		Scheme:                   mgr.GetScheme(),
		LocoNamespace:            seed.LocoNamespace,
		Gateway:                  seed.Gateway,
		WorkspaceNamespacePrefix: seed.WorkspaceNamespacePrefix,
		Telemetry:                seed.Telemetry,
		HealthyAfter:             seed.HealthyAfter,
	}
	Expect(r.SetupWithManager(mgr)).To(Succeed())
	managerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(managerCtx) }()
	DeferCleanup(func() { stop(); Eventually(done, routeManagerWait).Should(Receive(BeNil())) })
	Expect(mgr.GetCache().WaitForCacheSync(managerCtx)).To(BeTrue())
	return recorder
}
