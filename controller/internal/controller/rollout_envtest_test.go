package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Rollout", func() {
	It("keeps every old replica until a new one has stayed ready", func() {
		app := workspaceApp("rollout", "rollout")
		// Envtest never finishes deleting a namespace, so this spec needs its own.
		app.Spec.WorkspaceID = "rollout"
		namespace := &corev1.Namespace{}
		namespace.Name = testWorkspaceNamespace(app)
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client:                   k8sClient,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
			MinReadySeconds:          testMinReadySeconds,
		}
		_, err := r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())

		applied := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: namespace.Name, Name: testAppName(app)}
		Expect(k8sClient.Get(ctx, key, applied)).To(Succeed())
		Expect(applied.Spec.MinReadySeconds).To(Equal(testMinReadySeconds))
		rolling := applied.Spec.Strategy.RollingUpdate
		Expect(*rolling.MaxUnavailable).To(Equal(intstr.FromInt32(0)))
		Expect(*rolling.MaxSurge).To(Equal(intstr.FromString("25%")))
		readiness := applied.Spec.Template.Spec.Containers[0].ReadinessProbe
		Expect(readiness).NotTo(BeNil())
		Expect(readiness.TCPSocket).NotTo(BeNil())
		Expect(readiness.TCPSocket.Port).To(Equal(intstr.FromInt32(app.Spec.ServiceSpec.Deployment.Port)))
	})
})
