package controller

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Application autoscaling", func() {
	It("creates, updates and removes autoscalers without resetting HPA-owned replicas", func() {
		app := isolationTestApplication("ws-autoscaling", "autoscaling")
		reconciler := &ApplicationReconciler{
			Client:                   k8sClient,
			APIReader:                k8sClient,
			Scheme:                   scheme.Scheme,
			WorkspaceNamespacePrefix: "loco-ws-",
			Telemetry:                testTelemetry(),
		}
		Expect(ensureTestNamespace(ctx, k8sClient, app)).To(Succeed())
		resources := app.Spec.ServiceSpec.Resources
		resources.Replicas = locov1alpha1.ReplicasSpec{Min: 2, Max: 5}
		resources.Scalers = locov1alpha1.ScalersSpec{Enabled: true, CPUTarget: 70, MemoryTarget: 80}
		dep, err := reconciler.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		Expect(reconciler.ensureAutoscaler(ctx, app, dep)).To(Succeed())
		key := client.ObjectKey{Namespace: *dep.Namespace, Name: *dep.Name}
		hpa := &autoscalingv2.HorizontalPodAutoscaler{}
		Expect(k8sClient.Get(ctx, key, hpa)).To(Succeed())
		Expect(hpa.Spec.MinReplicas).To(HaveValue(Equal(int32(2))))
		Expect(hpa.Spec.MaxReplicas).To(Equal(int32(5)))
		Expect(
			hpa.Spec.ScaleTargetRef,
		).To(Equal(autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: key.Name}))
		Expect(hpa.Spec.Metrics).To(HaveLen(2))
		Expect(hpa.Spec.Metrics[0].Resource.Name).To(Equal(corev1.ResourceCPU))
		Expect(hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(HaveValue(Equal(int32(70))))
		Expect(hpa.Spec.Metrics[1].Resource.Name).To(Equal(corev1.ResourceMemory))
		Expect(hpa.Spec.Metrics[1].Resource.Target.AverageUtilization).To(HaveValue(Equal(int32(80))))
		Expect(metav1.GetControllerOf(hpa).UID).To(Equal(*dep.UID))

		deployment := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, deployment)).To(Succeed())
		for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			quantity := deployment.Spec.Template.Spec.Containers[0].Resources.Requests[name]
			Expect(quantity.IsZero()).To(BeFalse())
		}
		// The scale subresource uses the same field manager as Kubernetes' HPA controller.
		scale, err := json.Marshal(map[string]any{"spec": map[string]int32{"replicas": 4}})
		Expect(err).NotTo(HaveOccurred())
		Expect(
			k8sClient.SubResource("scale").
				Patch(ctx, deployment, client.RawPatch(client.Merge.Type(), scale), client.FieldOwner("horizontal-pod-autoscaler")),
		).To(Succeed())
		dep, err = reconciler.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		Expect(dep.Spec.Replicas).To(HaveValue(Equal(int32(4))))
		setReplicaCounts(app, dep)
		Expect(app.Status.DesiredReplicas).To(Equal(int32(4)))

		resources.Replicas.Max = 6
		resources.Scalers = locov1alpha1.ScalersSpec{Enabled: true, MemoryTarget: 60}
		Expect(reconciler.ensureAutoscaler(ctx, app, dep)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, hpa)).To(Succeed())
		Expect(hpa.Spec.MaxReplicas).To(Equal(int32(6)))
		Expect(hpa.Spec.Metrics).To(HaveLen(1))
		Expect(hpa.Spec.Metrics[0].Resource.Name).To(Equal(corev1.ResourceMemory))
		Expect(hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(HaveValue(Equal(int32(60))))

		resources.Scalers.Enabled = false
		dep, err = reconciler.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		Expect(reconciler.ensureAutoscaler(ctx, app, dep)).To(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, hpa))).To(BeTrue())
		Expect(dep.Spec.Replicas).To(HaveValue(Equal(int32(2))))

		resources.Scalers.Enabled = true
		Expect(reconciler.ensureAutoscaler(ctx, app, dep)).To(Succeed())
		Expect(reconciler.deleteAppObjects(ctx, app)).To(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, hpa))).To(BeTrue())
	})
})
