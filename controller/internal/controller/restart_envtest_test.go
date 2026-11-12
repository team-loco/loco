package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Replica restart", func() {
	It("rolls the pod template when only the placement revision changes", func() {
		app := workspaceApp("restart", "restart")
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "1"}
		namespace := &corev1.Namespace{Name: testWorkspaceNamespace(app)}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client: k8sClient, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix, Telemetry: testTelemetry(),
		}
		_, err := r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		key := client.ObjectKey{Namespace: namespace.Name, Name: testAppName(app)}
		before := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, before)).To(Succeed())
		Expect(before.Spec.Template.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "1"))

		app.Annotations[locov1alpha1.KeyPlacementRevision] = "2"
		_, err = r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		after := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, after)).To(Succeed())
		Expect(after.Spec.Template.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "2"))
		Expect(after.Generation).To(Equal(before.Generation + 1))
		Expect(after.Spec.Template.Spec).To(Equal(before.Spec.Template.Spec))
		Expect(after.Spec.Strategy.Type).To(Equal(appsv1.RollingUpdateDeploymentStrategyType))

		_, err = r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		unchanged := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, unchanged)).To(Succeed())
		Expect(unchanged.Generation).To(Equal(after.Generation))
	})
})
