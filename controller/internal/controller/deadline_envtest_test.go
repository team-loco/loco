package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const deadlineWorkspace = "ws-deadline"

func setUnschedulable(pod *corev1.Pod) {
	pod.Status.Conditions = []corev1.PodCondition{{
		Type:    corev1.PodScheduled,
		Status:  corev1.ConditionFalse,
		Reason:  corev1.PodReasonUnschedulable,
		Message: "0/1 nodes are available: insufficient memory",
	}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

// passDeadline moves the current revision's deploy deadline into the past.
func passDeadline(app *locov1alpha1.Application) {
	GinkgoHelper()
	Expect(app.Status.Rollout.Deadline).NotTo(BeNil())
	app.Status.Rollout.Deadline.At = metav1.NewTime(time.Now().Add(-time.Second))
	Expect(k8sClient.Status().Update(ctx, app)).To(Succeed())
}

func deadlineApp(name string, replicas int32) *locov1alpha1.Application {
	app := isolationTestApplication(deadlineWorkspace, name)
	app.Spec.ServiceSpec.Routing = nil
	app.Spec.ServiceSpec.Resources.Replicas = locov1alpha1.ReplicasSpec{Min: replicas, Max: replicas}
	app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
	return app
}

var _ = Describe("Application whose new revision misses its deploy timeout", func() {
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

	It("stops an unschedulable revision at its deadline and keeps the previous one serving", func() {
		app := deadlineApp("deadline-unschedulable", 2)
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		previous := createReplicaSet(app, "deadline-unschedulable-previous", "1", 2)
		current := createReplicaSet(app, "deadline-unschedulable-current", "2", 1)
		pod := createReplica(app, "deadline-unschedulable-replica")
		setUnschedulable(pod)

		started := time.Now()
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).To(BeNil())
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemUnschedulable))
		deadline := app.Status.Rollout.Deadline
		Expect(deadline).NotTo(BeNil())
		Expect(deadline.PlacementRevision).To(Equal(int64(2)))
		Expect(deadline.At.Time).To(BeTemporally("~", started.Add(testDeployTimeout*time.Second), 5*time.Second))

		By("keeping the deadline the first observation set")
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Deadline.At.Equal(&deadline.At)).To(BeTrue())
		Expect(replicaSetReplicas(current)).To(Equal(int32(1)))

		passDeadline(app)
		reconcileStuckApp(r, app)
		stopped := app.Status.Rollout.Stopped
		Expect(stopped).NotTo(BeNil())
		Expect(stopped.Reason).To(Equal(locov1alpha1.ProblemDeployTimeout))
		Expect(stopped.PlacementRevision).To(Equal(int64(2)))
		Expect(stopped.Rollout).To(BeTrue())
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemDeployTimeout))
		Expect(app.Status.Rollout.Problem.Detail).To(Equal("not healthy within 10 min · " +
			"0/1 nodes are available: insufficient memory"))
		dep := appDeployment(app)
		Expect(dep.Spec.Paused).To(BeTrue())
		Expect(replicaSetReplicas(current)).To(BeZero())
		Expect(replicaSetReplicas(previous)).To(Equal(int32(2)))
	})

	It("empties a revision whose image cannot be pulled at its deadline when nothing ran before it", func() {
		app := deadlineApp("deadline-pull", 1)
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		current := createReplicaSet(app, "deadline-pull-current", "2", 1)
		pod := createReplica(app, "deadline-pull-replica")
		setWaiting(pod, reasonImagePullBackOff, kubeletPullMessage)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemImagePullFailed))

		passDeadline(app)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).NotTo(BeNil())
		Expect(app.Status.Rollout.Stopped.Reason).To(Equal(locov1alpha1.ProblemDeployTimeout))
		Expect(app.Status.Rollout.Stopped.Rollout).To(BeFalse())
		Expect(app.Status.Rollout.Problem.Detail).To(ContainSubstring(kubeletPullMessage))
		Expect(*appDeployment(app).Spec.Replicas).To(BeZero())
		Expect(replicaSetReplicas(current)).To(BeZero())
	})

	It("lets a crash loop run under always until the deadline stops it", func() {
		app := deadlineApp("deadline-always", 1)
		app.Spec.ServiceSpec.RestartPolicy = locov1alpha1.RestartPolicySpec{Mode: locov1alpha1.RestartAlways}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		previous := createReplicaSet(app, "deadline-always-previous", "1", 1)
		pod := createReplica(app, "deadline-always-replica")
		restartTo(r, app, pod, locov1alpha1.MaxRestarts+2)
		Expect(app.Status.Rollout.Stopped).To(BeNil())
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemRestarting))

		passDeadline(app)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).NotTo(BeNil())
		Expect(app.Status.Rollout.Stopped.Reason).To(Equal(locov1alpha1.ProblemDeployTimeout))
		Expect(replicaSetReplicas(previous)).To(Equal(int32(1)))
	})

	It("stops a revision that restarted within its budget as a deploy timeout", func() {
		app := deadlineApp("deadline-restarted", 1)
		app.Spec.ServiceSpec.RestartPolicy = locov1alpha1.RestartPolicySpec{
			Mode: locov1alpha1.RestartOnFailure, MaxRestarts: 2,
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		createReplicaSet(app, "deadline-restarted-previous", "1", 1)
		pod := createReplica(app, "deadline-restarted-replica")
		restartTo(r, app, pod, 1)
		Expect(app.Status.Rollout.Stopped).To(BeNil())

		passDeadline(app)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Stopped).NotTo(BeNil())
		Expect(app.Status.Rollout.Stopped.Reason).To(Equal(locov1alpha1.ProblemDeployTimeout))
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemDeployTimeout))
	})

	It("holds a revision that became healthy to no deadline", func() {
		app := deadlineApp("deadline-healthy", 1)
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Deadline).NotTo(BeNil())
		pod := createReplica(app, "deadline-healthy-replica")
		setContainerState(pod, corev1.PodRunning, corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()},
		})
		pod.Status.ContainerStatuses[0].Ready = true
		pod.Status.Conditions = []corev1.PodCondition{
			{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
			{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now()},
		}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		dep := appDeployment(app)
		dep.Status.ObservedGeneration = dep.Generation
		dep.Status.Replicas, dep.Status.UpdatedReplicas, dep.Status.ReadyReplicas, dep.Status.AvailableReplicas = 1, 1, 1, 1
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Healthy).To(BeTrue())
		Expect(app.Status.Rollout.DeployedRevision).To(Equal(int64(2)))
		Expect(app.Status.Rollout.Deadline).To(BeNil())

		By("leaving a replica added later to wait for a node without stopping the revision")
		added := createReplica(app, "deadline-healthy-added")
		setUnschedulable(added)
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Problem.Reason).To(Equal(locov1alpha1.ProblemUnschedulable))
		Expect(app.Status.Rollout.Deadline).To(BeNil())
		Expect(app.Status.Rollout.Stopped).To(BeNil())

		By("setting a new deadline for the next placement revision")
		app.Annotations[locov1alpha1.KeyPlacementRevision] = "3"
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		reconcileStuckApp(r, app)
		Expect(app.Status.Rollout.Deadline).NotTo(BeNil())
		Expect(app.Status.Rollout.Deadline.PlacementRevision).To(Equal(int64(3)))
	})
})
