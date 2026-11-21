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

// createReplica creates a pod of the Application's placement revision.
func createReplica(app *locov1alpha1.Application, name string) *corev1.Pod {
	GinkgoHelper()
	return createReplicaAt(app, name, testImage, app.Annotations[locov1alpha1.KeyPlacementRevision])
}

// createReplicaAt creates a pod under a ReplicaSet recorded at the revision, creating that ReplicaSet
// when none exists.
func createReplicaAt(app *locov1alpha1.Application, name, image, revision string) *corev1.Pod {
	GinkgoHelper()
	dep := appDeployment(app)
	var sets appsv1.ReplicaSetList
	Expect(k8sClient.List(ctx, &sets, client.InNamespace(dep.Namespace),
		client.MatchingLabels{labelApp: testAppName(app)})).To(Succeed())
	var owner *appsv1.ReplicaSet
	for i := range sets.Items {
		if sets.Items[i].Annotations[locov1alpha1.KeyPlacementRevision] == revision {
			owner = &sets.Items[i]
		}
	}
	if owner == nil {
		owner = createReplicaSet(app, testAppName(app)+"-"+revision, revision, 1)
	}
	template := dep.Spec.Template
	ownerRef := metav1.NewControllerRef(owner, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))
	pod := &corev1.Pod{
		Name:            name,
		Namespace:       dep.Namespace,
		Labels:          template.Labels,
		Annotations:     template.Annotations,
		OwnerReferences: []metav1.OwnerReference{*ownerRef},
		Spec:            template.Spec,
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

		pod := createReplica(app, "root-replica")
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

		stale := createReplicaAt(app, "pull-stale", "registry.example.com/app:old", "1")
		setWaiting(stale, reasonCrashLoopBackOff, kubeletCrashMessage)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Available).To(BeFalse())

		current := createReplica(app, "pull-current")
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

	})
	It("reports another replica's startup failure while an image pull retries", func() {
		app := isolationTestApplication(stuckWorkspace, "mixed-pull")
		app.Spec.ServiceSpec.Routing = nil
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)

		pulling := createReplica(app, "mixed-a-pulling")
		setWaiting(pulling, reasonErrImagePull, kubeletPullMessage)
		broken := createReplica(app, "mixed-z-broken")
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
		pod := createReplica(app, "schedule-current")
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

	It("persists public runtime conditions and clears them on recovery", func() {
		app := isolationTestApplication(stuckWorkspace, "runtime")
		app.Spec.ServiceSpec.Routing = nil
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		pod := createReplica(app, "runtime-current")
		for _, tc := range []struct {
			reason string
			want   locov1alpha1.ProblemReason
		}{
			{reasonCrashLoopBackOff, locov1alpha1.ProblemRestarting},
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
