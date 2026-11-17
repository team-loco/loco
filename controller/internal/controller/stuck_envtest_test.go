package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	stuckWorkspace          = "ws-stuck"
	kubeletRootImageMessage = "container has runAsNonRoot and image will run as root " +
		"(pod: \"resource-root_ws-stuck(1)\", container: resource-root)"
	kubeletPullMessage  = "Back-off pulling image \"registry.example.com/app:v1\""
	kubeletCrashMessage = "back-off 5m0s restarting failed container"
)

func reconcileStuckApp(r *ApplicationReconciler, app *locov1alpha1.Application) {
	key := client.ObjectKeyFromObject(app)
	req := reconcile.Request{NamespacedName: key}
	_, err := r.Reconcile(ctx, req)
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
}

func createReplica(app *locov1alpha1.Application, name, image string) *corev1.Pod {
	dep := &appsv1.Deployment{}
	depKey := client.ObjectKey{
		Namespace: testEnvironmentNamespace(app),
		Name:      testAppName(app),
	}
	Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
	template := dep.Spec.Template
	pod := &corev1.Pod{
		Name:        name,
		Namespace:   depKey.Namespace,
		Labels:      template.Labels,
		Annotations: template.Annotations,
		Spec:        template.Spec,
	}
	pod.Spec.Containers[0].Image = image
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}

func setContainerState(pod *corev1.Pod, phase corev1.PodPhase, state corev1.ContainerState) {
	container := pod.Spec.Containers[0]
	pod.Status.Phase = phase
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:  container.Name,
		Image: container.Image,
		State: state,
	}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

func setWaiting(pod *corev1.Pod, reason, message string) {
	waiting := &corev1.ContainerStateWaiting{Reason: reason, Message: message}
	state := corev1.ContainerState{Waiting: waiting}
	setContainerState(pod, corev1.PodPending, state)
}

var _ = Describe("Application with replicas that cannot start", func() {
	var r *ApplicationReconciler

	BeforeEach(func() {
		Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed())
		r = &ApplicationReconciler{
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Client:                   k8sClient,
			APIReader:                k8sClient,
			Scheme:                   k8sClient.Scheme(),
			LocoNamespace:            testNamespace,
			Telemetry:                testTelemetry(),
		}
	})

	It("fails an image that runs as root and recovers once the replica starts", func() {
		app := isolationTestApplication(stuckWorkspace, "root")
		app.Spec.ServiceSpec.Routing = nil
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Available).To(BeFalse())

		pod := createReplica(app, "root-replica", testImage)
		setWaiting(pod, reasonCreateContainerConfigError, kubeletRootImageMessage)
		reconcileStuckApp(r, app)

		Expect(app.Status.Rollout.Problem).NotTo(BeNil())
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemImageUser))
		Expect(app.Status.Rollout.Problem.Detail).To(ContainSubstring(kubeletRootImageMessage))

		started := metav1.Now()
		running := &corev1.ContainerStateRunning{StartedAt: started}
		state := corev1.ContainerState{Running: running}
		setContainerState(pod, corev1.PodRunning, state)
		reconcileStuckApp(r, app)

		Expect(app.Status.Rollout.Available).To(BeFalse())
		Expect(app.Status.Rollout.Problem).To(BeNil())
	})

	It("reports image pull problems for the current revision", func() {
		app := isolationTestApplication(stuckWorkspace, "pull")
		app.Spec.ServiceSpec.Routing = nil
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)

		stale := createReplica(app, "pull-stale", "registry.example.com/app:old")
		stale.Annotations[locov1alpha1.KeyPlacementRevision] = "1"
		Expect(k8sClient.Update(ctx, stale)).To(Succeed())
		setWaiting(stale, reasonCrashLoopBackOff, kubeletCrashMessage)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Available).To(BeFalse())

		current := createReplica(app, "pull-current", testImage)
		for _, reason := range []string{reasonErrImagePull, reasonImagePullBackOff} {
			setWaiting(current, reason, kubeletPullMessage)
			reconcileStuckApp(r, app)
			Expect(app.Status.Rollout.Available).To(BeFalse())
			Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemImagePullFailed))
		}
		setContainerState(current, corev1.PodRunning, corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()},
		})
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Available).To(BeFalse())
		Expect(app.Status.Rollout.Problem).To(BeNil())

		setWaiting(current, reasonImagePullBackOff, kubeletPullMessage)
		dep := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: testAppName(app)}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		dep.Status.ObservedGeneration = dep.Generation
		dep.Status.Conditions = []appsv1.DeploymentCondition{{
			Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse,
			Reason: reasonProgressDeadlineExceeded,
		}}
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		reconcileStuckApp(r, app)

		Expect(app.Status.Rollout.Problem).NotTo(BeNil())
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemImagePullFailed))
	})
	It("reports another replica's startup failure while an image pull retries", func() {
		app := isolationTestApplication(stuckWorkspace, "mixed-pull")
		app.Spec.ServiceSpec.Routing = nil
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)

		pulling := createReplica(app, "mixed-a-pulling", testImage)
		setWaiting(pulling, reasonErrImagePull, kubeletPullMessage)
		broken := createReplica(app, "mixed-z-broken", testImage)
		setWaiting(broken, reasonCreateContainerConfigError, kubeletRootImageMessage)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem).NotTo(BeNil())
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemImageUser))

		Expect(k8sClient.Delete(ctx, broken)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Available).To(BeFalse())
	})

	It("fails an unschedulable replica and resumes when scheduling succeeds", func() {
		app := isolationTestApplication(stuckWorkspace, "schedule")
		app.Spec.ServiceSpec.Routing = nil
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		pod := createReplica(app, "schedule-current", testImage)
		pod.Status.Conditions = []corev1.PodCondition{{
			Type:    corev1.PodScheduled,
			Status:  corev1.ConditionFalse,
			Reason:  corev1.PodReasonUnschedulable,
			Message: "0/1 nodes are available: insufficient memory",
		}}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem).NotTo(BeNil())
		Expect(app.Status.Rollout.Problem.Detail).To(ContainSubstring("insufficient memory"))
		pod.Status.Conditions[0].Status = corev1.ConditionTrue
		pod.Status.Conditions[0].Reason = "Scheduled"
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Available).To(BeFalse())
		Expect(app.Status.Rollout.Problem).To(BeNil())
	})

	It("fails an expired rollout deadline and ignores conditions from an older generation", func() {
		app := isolationTestApplication(stuckWorkspace, "deadline")
		app.Spec.ServiceSpec.Routing = nil
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		dep := &appsv1.Deployment{}
		key := client.ObjectKey{
			Namespace: testEnvironmentNamespace(app),
			Name:      testAppName(app),
		}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		dep.Status.ObservedGeneration = dep.Generation
		dep.Status.Conditions = []appsv1.DeploymentCondition{{
			Type:    appsv1.DeploymentProgressing,
			Status:  corev1.ConditionFalse,
			Reason:  "ProgressDeadlineExceeded",
			Message: "Deployment exceeded its progress deadline",
		}}
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem).NotTo(BeNil())
		Expect(app.Status.Rollout.Problem.Detail).To(ContainSubstring("progress deadline"))
		dep.Status.ObservedGeneration = dep.Generation - 1
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Available).To(BeFalse())
		Expect(app.Status.Rollout.Problem).To(BeNil())
		dep.Status.ObservedGeneration = dep.Generation
		dep.Status.Conditions[0].Status = corev1.ConditionTrue
		dep.Status.Conditions[0].Reason = "NewReplicaSetAvailable"
		dep.Status.Replicas = *dep.Spec.Replicas
		dep.Status.UpdatedReplicas = *dep.Spec.Replicas
		dep.Status.AvailableReplicas = *dep.Spec.Replicas
		dep.Status.ReadyReplicas = *dep.Spec.Replicas
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Available).To(BeTrue())
		Expect(app.Status.Rollout.Problem).To(BeNil())

	})

	It("persists public runtime conditions and clears them on recovery", func() {
		app := isolationTestApplication(stuckWorkspace, "runtime")
		app.Spec.ServiceSpec.Routing = nil
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		pod := createReplica(app, "runtime-current", testImage)
		for _, tc := range []struct {
			reason string
			want   locov1alpha1.ProblemReason
		}{
			{reasonCrashLoopBackOff, locov1alpha1.ProblemCrashLoop},
			{reasonImagePullBackOff, locov1alpha1.ProblemImagePullFailed},
		} {
			setWaiting(pod, tc.reason, "private replica diagnostics")
			reconcileStuckApp(r, app)
			Expect(app.Status.Rollout.Problem.Reason).To(Equal(tc.want))
			Expect(app.Status.Rollout.Problem.Detail).To(ContainSubstring("private replica diagnostics"))
		}
		pod.Status.ContainerStatuses[0].State = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: reasonOOMKilled, ExitCode: 137},
		}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemOutOfMemory))

		pod.Status.ContainerStatuses = nil
		pod.Status.Conditions = []corev1.PodCondition{
			{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable},
		}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemUnschedulable))

		// A running container with a completed startup and a failing readiness check.
		pod.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/runtime-health",
				Port: intstr.FromInt32(*app.Spec.ServiceSpec.Deployment.Port),
			},
		}
		// Pod probes are immutable: create another current replica with the probe.
		Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
		pod.Name = "runtime-health"
		pod.ResourceVersion = ""
		pod.UID = ""
		pod.Status = corev1.PodStatus{}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		started := true
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{
			{
				Name:    pod.Spec.Containers[0].Name,
				Started: &started,
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(time.Now().Add(-time.Hour))},
				},
			},
		}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemHealthCheckFailing))
		pod.Status.ContainerStatuses[0].Ready = true
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem).To(BeNil())
	})

})
