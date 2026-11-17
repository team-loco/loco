package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		Expect(app.Status.Rollout.Replicas[0].Version).To(Equal(loco.ReplicaCurrent))
		Expect(app.Status.Rollout.Replicas[2].Version).To(Equal(loco.ReplicaPrevious))
		dep.Status.WithReplicas(2)
		Expect(r.observeReplicas(ctx, app, dep, time.Now())).To(Succeed())
		Expect(app.Status.Rollout.Available).To(BeTrue())
	})
})
