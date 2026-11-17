package controller

import (
	"context"
	"net/http"
	"time"

	"k8s.io/client-go/rest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var _ = Describe("Replica rollout reports", func() {
	It("reports both revisions and waits for all current replicas", func() {
		app := workspaceApp("replica-report", "replica-report")
		app.Spec.WorkspaceID = "replica-report"
		app.Annotations = map[string]string{loco.KeyPlacementRevision: "2"}
		app.Status.ObservedPlacementRevision = 2
		ns := &corev1.Namespace{Name: testWorkspaceNamespace(app)}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, ns)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client:                   k8sClient,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
			MinReadySeconds:          testMinReadySeconds,
			DrainSeconds:             testDrainSeconds,
		}
		_, err := r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		for _, name := range []string{"old", "new-a", "new-b"} {
			pod := createReplica(app, name, "app:v1")
			if name == "old" {
				pod.Annotations[loco.KeyPlacementRevision] = "1"
				Expect(k8sClient.Update(ctx, pod)).To(Succeed())
			}
			pod.Status.Conditions = []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		}
		stored := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: testAppName(app)}, stored)).To(Succeed())
		dep := appsv1ac.Deployment(stored.Name, stored.Namespace).
			WithGeneration(stored.Generation).
			WithSpec(appsv1ac.DeploymentSpec().
				WithReplicas(2)).
			WithStatus(appsv1ac.DeploymentStatus().
				WithObservedGeneration(stored.Generation).
				WithUpdatedReplicas(2).
				WithAvailableReplicas(2).
				WithReplicas(3))
		Expect(r.observeReplicas(ctx, app, dep, time.Now())).To(Succeed())
		Expect(app.Status.Rollout.Available).To(BeFalse())
		Expect(app.Status.Rollout.Replicas).To(HaveLen(3))
		Expect(app.Status.Rollout.Replicas[0].PlacementRevision).To(Equal(int64(2)))
		Expect(app.Status.Rollout.Replicas[2].PlacementRevision).To(Equal(int64(1)))
		dep.Status.WithReplicas(2)
		Expect(r.observeReplicas(ctx, app, dep, time.Now())).To(Succeed())
		Expect(app.Status.Rollout.Available).To(BeTrue())
	})
})

var _ = Describe("Pull event watch", func() {
	It("reports Pulling from regarding and updates to Starting after Pulled", func() {
		observed := &pullWatchTransport{active: make(map[string]int), selectors: make(map[string][]string)}
		managerConfig := rest.CopyConfig(cfg)
		managerConfig.Wrap(func(base http.RoundTripper) http.RoundTripper {
			observed.base = base
			return observed
		})
		mgr, err := ctrl.NewManager(managerConfig, ctrl.Options{
			Scheme: k8sClient.Scheme(), Cache: CacheOptions(testNamespace, "", false),
			Controller: config.Controller{SkipNameValidation: new(true)},
			Metrics:    metricsserver.Options{BindAddress: disabledListener}, HealthProbeBindAddress: disabledListener,
		})
		Expect(err).NotTo(HaveOccurred())
		r := &ApplicationReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Scheme: mgr.GetScheme(),
			LocoNamespace: testNamespace, Gateway: testGateway, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry: testTelemetry()}
		Expect(r.SetupWithManager(mgr)).To(Succeed())
		managerCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- mgr.Start(managerCtx) }()
		DeferCleanup(func() { stop(); Eventually(done, routeManagerWait).Should(Receive(BeNil())) })
		Expect(mgr.GetCache().WaitForCacheSync(managerCtx)).To(BeTrue())
		app := isolationTestApplication("ws-pull-watch", "pull-watch")
		app.Annotations = map[string]string{loco.KeyPlacementRevision: "1"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(ensureTestNamespace(ctx, k8sClient, app)).To(Succeed())
		seed := &ApplicationReconciler{
			Client:                   k8sClient,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
		}
		_, err = seed.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		pod := createReplica(app, "pulling-pod", testImage)
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}}
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: pod.Spec.Containers[0].Name,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reasonContainerCreating}}}}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		key := client.ObjectKeyFromObject(app)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
			g.Expect(app.Status.Rollout).NotTo(BeNil())
			g.Expect(app.Status.Rollout.Replicas).To(HaveLen(1))
			g.Expect(app.Status.Rollout.Replicas[0].State).To(Equal(loco.ReplicaStarting))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		at := time.Now().Truncate(time.Microsecond)
		pull := &eventsv1.Event{
			Name:                "pulling",
			Namespace:           pod.Namespace,
			Reason:              pullReasonPulling,
			Action:              pullReasonPulling,
			Type:                corev1.EventTypeNormal,
			ReportingController: "kubernetes.io/kubelet",
			ReportingInstance:   "node",
			EventTime:           metav1.NewMicroTime(at),
			Regarding: corev1.ObjectReference{
				Kind:       "Pod",
				APIVersion: "v1",
				Name:       pod.Name,
				Namespace:  pod.Namespace,
				UID:        pod.UID,
			},
		}
		Eventually(
			func() int { return observed.watches(pod.Namespace) },
			routeManagerWait,
			routeManagerPoll,
		).Should(BeNumerically(">", 0))
		Expect(k8sClient.Create(ctx, pull)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
			g.Expect(app.Status.Rollout.Replicas[0].State).To(Equal(loco.ReplicaPulling))
			g.Expect(app.Status.Rollout.Replicas[0].StateSince.Time.Unix()).To(Equal(at.Unix()))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		pull.Name, pull.ResourceVersion, pull.UID = "pulled", "", ""
		pull.Reason, pull.Action = pullReasonPulled, pullReasonPulled
		pull.EventTime = metav1.NewMicroTime(at.Add(time.Second))
		pull.Note = "Successfully pulled image in 1s. Image size: 42 bytes."
		Expect(k8sClient.Create(ctx, pull)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
			g.Expect(app.Status.Rollout.Replicas[0].State).To(Equal(loco.ReplicaStarting))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		outside := &corev1.Namespace{Name: testWorkspaceNamespacePrefix + "outside-pulls"}
		Expect(k8sClient.Create(ctx, outside)).To(Succeed())
		outsidePod := pod.DeepCopy()
		outsidePod.Namespace, outsidePod.UID, outsidePod.ResourceVersion = outside.Name, "", ""
		Expect(k8sClient.Create(ctx, outsidePod)).To(Succeed())
		outsideEvent := pull.DeepCopy()
		outsideEvent.Namespace, outsideEvent.UID, outsideEvent.ResourceVersion = outside.Name, "", ""
		outsideEvent.Regarding.Namespace, outsideEvent.Regarding.UID = outside.Name, outsidePod.UID
		Expect(k8sClient.Create(ctx, outsideEvent)).To(Succeed())
		Consistently(func() bool {
			r.pullMu.Lock()
			defer r.pullMu.Unlock()
			_, found := r.pullEvents[outsidePod.UID]
			return found
		}, 200*time.Millisecond, routeManagerPoll).Should(BeFalse())
		observed.expectScoped(pod.Namespace, outside.Name)
		ns := &corev1.Namespace{Name: pod.Namespace}
		Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		Eventually(
			func() int { return observed.watches(pod.Namespace) },
			routeManagerWait,
			routeManagerPoll,
		).Should(Equal(0))
		Eventually(func() bool {
			r.pullMu.Lock()
			defer r.pullMu.Unlock()
			_, found := r.pullEvents[pod.UID]
			return found
		}, routeManagerWait, routeManagerPoll).Should(BeFalse())
		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
	})
})
