package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func reconcileStuckApp(r *LocoResourceReconciler, app *locov1alpha1.Application) {
	key := client.ObjectKeyFromObject(app)
	req := reconcile.Request{NamespacedName: key}
	_, err := r.Reconcile(ctx, req)
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
}

func createReplica(app *locov1alpha1.Application, name, image string) *corev1.Pod {
	dep := &appsv1.Deployment{}
	depKey := client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}
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
	var r *LocoResourceReconciler

	BeforeEach(func() {
		Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed())
		r = &LocoResourceReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	It("fails an image that runs as root and recovers once the replica starts", func() {
		app := isolationTestApplication(stuckWorkspace, "root")
		app.Spec.ServiceSpec.Routing = nil
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Phase).To(Equal(phaseDeploying))

		pod := createReplica(app, "root-replica", testImage)
		setWaiting(pod, reasonCreateContainerConfigError, kubeletRootImageMessage)
		reconcileStuckApp(r, app)

		Expect(app.Status.Phase).To(Equal(phaseFailed))
		Expect(app.Status.Message).To(Equal(
			"The image runs as root. Loco runs containers as a non-root user; use an image that sets a non-root USER.",
		))
		degraded := meta.FindStatusCondition(app.Status.Conditions, conditionDegraded)
		Expect(degraded).NotTo(BeNil())
		Expect(degraded.Status).To(Equal(metav1.ConditionTrue))
		Expect(degraded.Reason).To(Equal(reasonCreateContainerConfigError))
		Expect(degraded.Message).To(Equal(kubeletRootImageMessage))

		started := metav1.Now()
		running := &corev1.ContainerStateRunning{StartedAt: started}
		state := corev1.ContainerState{Running: running}
		setContainerState(pod, corev1.PodRunning, state)
		reconcileStuckApp(r, app)

		Expect(app.Status.Phase).To(Equal(phaseDeploying))
		Expect(meta.FindStatusCondition(app.Status.Conditions, conditionDegraded)).To(BeNil())
	})

	It("fails an image that cannot be pulled and ignores replicas of an older image", func() {
		app := isolationTestApplication(stuckWorkspace, "pull")
		app.Spec.ServiceSpec.Routing = nil
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)

		stale := createReplica(app, "pull-stale", "registry.example.com/app:old")
		setWaiting(stale, reasonCrashLoopBackOff, kubeletCrashMessage)
		reconcileStuckApp(r, app)
		Expect(app.Status.Phase).To(Equal(phaseDeploying))

		current := createReplica(app, "pull-current", testImage)
		setWaiting(current, reasonImagePullBackOff, kubeletPullMessage)
		reconcileStuckApp(r, app)

		Expect(app.Status.Phase).To(Equal(phaseFailed))
		Expect(app.Status.Message).To(Equal(messageImagePullFailed))
		degraded := meta.FindStatusCondition(app.Status.Conditions, conditionDegraded)
		Expect(degraded).NotTo(BeNil())
		Expect(degraded.Reason).To(Equal(reasonImagePullBackOff))
	})
})
