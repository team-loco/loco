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
			DrainSeconds:             testDrainSeconds,
		}
		_, err := r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())

		applied := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: namespace.Name, Name: testAppName(app)}
		Expect(k8sClient.Get(ctx, key, applied)).To(Succeed())
		Expect(applied.Spec.MinReadySeconds).To(Equal(testMinReadySeconds))
		Expect(*applied.Spec.Template.Spec.TerminationGracePeriodSeconds).To(
			Equal(int64(corev1.DefaultTerminationGracePeriodSeconds) + int64(testDrainSeconds)),
		)
		lifecycle := applied.Spec.Template.Spec.Containers[0].Lifecycle
		Expect(lifecycle).NotTo(BeNil())
		Expect(lifecycle.PreStop).NotTo(BeNil())
		Expect(lifecycle.PreStop.Sleep).NotTo(BeNil())
		Expect(lifecycle.PreStop.Sleep.Seconds).To(Equal(int64(testDrainSeconds)))
		rolling := applied.Spec.Strategy.RollingUpdate
		Expect(*rolling.MaxUnavailable).To(Equal(intstr.FromInt32(0)))
		Expect(*rolling.MaxSurge).To(Equal(intstr.FromString("25%")))
		readiness := applied.Spec.Template.Spec.Containers[0].ReadinessProbe
		Expect(readiness).NotTo(BeNil())
		Expect(readiness.TCPSocket).NotTo(BeNil())
		Expect(readiness.TCPSocket.Port).To(Equal(intstr.FromInt32(*app.Spec.ServiceSpec.Deployment.Port)))
	})
	It("gives a service without a port no probe but the same rollout", func() {
		app := workspaceApp("rollout-worker", "rollout-worker")
		app.Spec.WorkspaceID = "rollout-worker"
		app.Spec.ServiceSpec.Deployment.Port = nil
		namespace := &corev1.Namespace{}
		namespace.Name = testWorkspaceNamespace(app)
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r := &ApplicationReconciler{
			Client:                   k8sClient,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
			MinReadySeconds:          testMinReadySeconds,
			DrainSeconds:             testDrainSeconds,
		}
		_, err := r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())

		applied := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: namespace.Name, Name: testAppName(app)}
		Expect(k8sClient.Get(ctx, key, applied)).To(Succeed())
		Expect(applied.Spec.MinReadySeconds).To(Equal(testMinReadySeconds))
		Expect(*applied.Spec.Strategy.RollingUpdate.MaxUnavailable).To(Equal(intstr.FromInt32(0)))
		container := applied.Spec.Template.Spec.Containers[0]
		Expect(container.Ports).To(BeEmpty())
		Expect(container.Lifecycle).To(BeNil())
		Expect(
			*applied.Spec.Template.Spec.TerminationGracePeriodSeconds,
		).To(Equal(int64(corev1.DefaultTerminationGracePeriodSeconds)))
		Expect(container.ReadinessProbe).To(BeNil())
		Expect(container.LivenessProbe).To(BeNil())
	})
})
