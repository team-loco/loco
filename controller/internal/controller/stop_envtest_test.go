package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const stopWorkspace = "ws-stop"

func appDeployment(app *locov1alpha1.Application) *appsv1.Deployment {
	dep := &appsv1.Deployment{}
	key := client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: testAppName(app)}
	Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
	return dep
}

// createReplicaSet stands in for the Deployment controller, which envtest does not run.
func createReplicaSet(app *locov1alpha1.Application, name, revision string, replicas int32) *appsv1.ReplicaSet {
	dep := appDeployment(app)
	template := *dep.Spec.Template.DeepCopy()
	template.Annotations[locov1alpha1.KeyPlacementRevision] = revision
	set := &appsv1.ReplicaSet{
		Name:      name,
		Namespace: dep.Namespace,
		Labels:    template.Labels,
		Spec: appsv1.ReplicaSetSpec{
			Replicas: new(replicas),
			Selector: dep.Spec.Selector,
			Template: template,
		},
	}
	Expect(k8sClient.Create(ctx, set)).To(Succeed())
	return set
}

func replicaSetReplicas(set *appsv1.ReplicaSet) int32 {
	current := &appsv1.ReplicaSet{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(set), current)).To(Succeed())
	return ptr.Deref(current.Spec.Replicas, 0)
}

func setRestarted(pod *corev1.Pod, restarts int32) {
	pod.Status.Phase = corev1.PodRunning
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:         pod.Spec.Containers[0].Name,
		Image:        pod.Spec.Containers[0].Image,
		RestartCount: restarts,
		State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: "Error", ExitCode: 1, FinishedAt: metav1.Now(),
		}},
	}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

var _ = Describe("Application whose replica used up its restart budget", func() {
	var r *ApplicationReconciler

	BeforeEach(func() {
		r = &ApplicationReconciler{
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Client:                   k8sClient,
			APIReader:                k8sClient,
			Scheme:                   k8sClient.Scheme(),
			LocoNamespace:            testNamespace,
			Telemetry:                testTelemetry(),
			HealthyAfter:             testHealthyAfter,
		}
	})

	It("empties the new revision and keeps the previous one serving during a rollout", func() {
		app := isolationTestApplication(stopWorkspace, "stop-rollout")
		app.Spec.ServiceSpec.Routing = nil
		app.Spec.ServiceSpec.Resources.Replicas = locov1alpha1.ReplicasSpec{Min: 2, Max: 2}
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		previous := createReplicaSet(app, "stop-rollout-previous", "1", 2)
		current := createReplicaSet(app, "stop-rollout-current", "2", 1)

		pod := createReplica(app, "stop-rollout-replica", testImage)
		setRestarted(pod, testRestartPolicy.MaxRestarts)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).To(BeNil())
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemRestarting))
		Expect(replicaSetReplicas(current)).To(Equal(int32(1)))

		setRestarted(pod, testRestartPolicy.MaxRestarts+1)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).NotTo(BeNil())
		Expect(app.Status.Rollout.Stopped.PlacementRevision).To(Equal(int64(2)))
		Expect(app.Status.Rollout.Stopped.Replica).To(Equal(pod.Name))
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemRestartsExhausted))
		Expect(app.Status.Rollout.Available).To(BeFalse())
		dep := appDeployment(app)
		Expect(dep.Spec.Paused).To(BeTrue())
		Expect(*dep.Spec.Replicas).To(Equal(int32(2)))
		Expect(replicaSetReplicas(current)).To(BeZero())
		Expect(replicaSetReplicas(previous)).To(Equal(int32(2)))

		By("emptying the stopped revision again on every reconcile")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(current), current)).To(Succeed())
		current.Spec.Replicas = new(int32(1))
		Expect(k8sClient.Update(ctx, current)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(replicaSetReplicas(current)).To(BeZero())
		Expect(app.Status.Rollout.Stopped).NotTo(BeNil())
	})

	It("stops the service at steady state and starts it again on a new placement revision", func() {
		app := isolationTestApplication(stopWorkspace, "stop-steady")
		app.Spec.ServiceSpec.Routing = nil
		app.Spec.ServiceSpec.Resources.Replicas = locov1alpha1.ReplicasSpec{Min: 1, Max: 3}
		app.Spec.ServiceSpec.Resources.Scalers = locov1alpha1.ScalersSpec{Enabled: true, CPUTarget: 50}
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		hpaKey := client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: testAppName(app)}
		Expect(k8sClient.Get(ctx, hpaKey, &autoscalingv2.HorizontalPodAutoscaler{})).To(Succeed())
		createReplicaSet(app, "stop-steady-current", "2", 1)

		pod := createReplica(app, "stop-steady-replica", testImage)
		setRestarted(pod, testRestartPolicy.MaxRestarts+1)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).NotTo(BeNil())
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemRestartsExhausted))
		Expect(app.Status.Rollout.DesiredReplicas).To(Equal(int32(1)))
		dep := appDeployment(app)
		Expect(dep.Spec.Paused).To(BeTrue())
		Expect(*dep.Spec.Replicas).To(BeZero())
		err := k8sClient.Get(ctx, hpaKey, &autoscalingv2.HorizontalPodAutoscaler{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "autoscaler of a stopped service: %v", err)

		By("rendering the same stopped shape while the revision stays")
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).NotTo(BeNil())
		Expect(app.Status.Rollout.DesiredReplicas).To(Equal(int32(1)))
		Expect(*appDeployment(app).Spec.Replicas).To(BeZero())

		By("starting a new placement revision")
		app.Annotations[locov1alpha1.KeyPlacementRevision] = "3"
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).To(BeNil())
		dep = appDeployment(app)
		Expect(dep.Spec.Paused).To(BeFalse())
		Expect(*dep.Spec.Replicas).To(Equal(int32(1)))
		Expect(k8sClient.Get(ctx, hpaKey, &autoscalingv2.HorizontalPodAutoscaler{})).To(Succeed())
	})
})
