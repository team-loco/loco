package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// createSetReplica stands in for the ReplicaSet controller, which envtest does not run.
func createSetReplica(set *appsv1.ReplicaSet, name string) *corev1.Pod {
	GinkgoHelper()
	pod := &corev1.Pod{
		Name:        name,
		Namespace:   set.Namespace,
		Labels:      set.Spec.Template.Labels,
		Annotations: set.Spec.Template.Annotations,
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(set, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
		},
		Spec: set.Spec.Template.Spec,
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}

var _ = Describe("Placement returning to the previous deployment", func() {
	It("keeps the previous deployment's pods and still restarts them on a later revision", func() {
		app := workspaceApp("revert", "revert")
		app.Spec.EnvironmentID = "env-revert"
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "1"}
		namespace := &corev1.Namespace{Name: testEnvironmentNamespace(app)}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client: k8sClient, APIReader: k8sClient,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix, Telemetry: testTelemetry(),
		}
		previousSpec := app.Spec.DeepCopy()
		_, err := r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		previous := createReplicaSet(app, "revert-previous", "1", 1)
		serving := createSetReplica(previous, "revert-previous-replica")

		By("rolling out a new deployment that never becomes ready")
		app.Spec.DeploymentID = "next"
		app.Spec.ServiceSpec.Deployment.Image = testImage + "-next"
		app.Annotations[locov1alpha1.KeyPlacementRevision] = "2"
		_, err = r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		createReplicaSet(app, "revert-next", "2", 1)

		By("placing the previous deployment again")
		app.Spec = *previousSpec
		app.Annotations[locov1alpha1.KeyPlacementRevision] = "3"
		_, err = r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		dep := appDeployment(app)
		Expect(dep.Spec.Template.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "3"))
		revived := &appsv1.ReplicaSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(previous), revived)).To(Succeed())
		Expect(revived.Spec.Template.Annotations).To(Equal(dep.Spec.Template.Annotations))
		kept := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(serving), kept)).To(Succeed())
		Expect(kept.UID).To(Equal(serving.UID))
		Expect(kept.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "3"))

		By("restarting the placement")
		app.Annotations[locov1alpha1.KeyPlacementRevision] = "4"
		_, err = r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		Expect(appDeployment(app).Spec.Template.Annotations).
			To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "4"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(previous), revived)).To(Succeed())
		Expect(revived.Spec.Template.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "3"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(serving), kept)).To(Succeed())
		Expect(kept.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "3"))
	})
	It("replaces the previous deployment's pods when its secret values changed", func() {
		app := workspaceApp("revert-secret", "revert-secret")
		app.Spec.EnvironmentID = "env-revert-secret"
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "1"}
		namespace := &corev1.Namespace{Name: testEnvironmentNamespace(app)}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client: k8sClient, APIReader: k8sClient,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix, Telemetry: testTelemetry(),
		}
		secret := func(revision int64, value string) envSecretMount {
			return envSecretMount{Mounted: true, Revision: revision, Names: []string{"TOKEN"}, Digest: []byte(value)}
		}
		previousSpec := app.Spec.DeepCopy()
		_, err := r.ensureDeployment(ctx, app, secret(1, "old"))
		Expect(err).NotTo(HaveOccurred())
		previous := createReplicaSet(app, "revert-secret-previous", "1", 1)
		serving := createSetReplica(previous, "revert-secret-previous-replica")

		app.Spec.DeploymentID = "next"
		app.Spec.ServiceSpec.Deployment.Image = testImage + "-next"
		app.Annotations[locov1alpha1.KeyPlacementRevision] = "2"
		_, err = r.ensureDeployment(ctx, app, secret(2, "old"))
		Expect(err).NotTo(HaveOccurred())

		By("placing the previous deployment again with a changed secret value")
		app.Spec = *previousSpec
		app.Annotations[locov1alpha1.KeyPlacementRevision] = "3"
		_, err = r.ensureDeployment(ctx, app, secret(3, "new"))
		Expect(err).NotTo(HaveOccurred())
		Expect(appDeployment(app).Spec.Template.Annotations).
			To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "3"))
		kept := &appsv1.ReplicaSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(previous), kept)).To(Succeed())
		Expect(kept.Spec.Template.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "1"))
		pod := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(serving), pod)).To(Succeed())
		Expect(pod.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "1"))
	})
})
