package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Workspace namespace deletion", func() {
	It("converges an Application that arrives while its namespace terminates", func() {
		pullSecret := &corev1.Secret{
			Name: testPullSecretName, Namespace: testNamespace,
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, pullSecret))).To(Succeed())

		const name = "terminating"
		app := testApplication()
		app.Name, app.Namespace = name, testNamespace
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementID: "p-terminating"}
		app.Spec.ResourceID, app.Spec.Slug, app.Spec.WorkspaceID = name, name, "ws-terminating"
		app.Spec.EnvironmentID = "env-terminating"
		app.Spec.ServiceSpec.Deployment.EnvSecretRef = nil
		namespaceName := testEnvironmentNamespace(app)

		Expect(k8sClient.Create(ctx, &corev1.Namespace{Name: namespaceName})).To(Succeed())
		Expect(k8sClient.Delete(ctx, &corev1.Namespace{Name: namespaceName})).To(Succeed())
		terminating := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: namespaceName}, terminating)).To(Succeed())
		Expect(terminating.Status.Phase).To(Equal(corev1.NamespaceTerminating))

		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		r := &ApplicationReconciler{
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Client:                   k8sClient,
			APIReader:                k8sClient,
			Scheme:                   k8sClient.Scheme(),
			LocoNamespace:            testNamespace,
			Telemetry:                testTelemetry(),
			PullSecretName:           testPullSecretName,
		}
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		depKey := client.ObjectKey{Namespace: namespaceName, Name: testAppName(app)}

		_, err := r.Reconcile(ctx, req)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, depKey, &appsv1.Deployment{}))).To(BeTrue())

		// The test cluster has no namespace controller, so the namespace is finalized here.
		clientset, err := kubernetes.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: namespaceName}, terminating)).To(Succeed())
		terminating.Spec.Finalizers = nil
		_, err = clientset.CoreV1().Namespaces().Finalize(ctx, terminating, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKey{Name: namespaceName}, &corev1.Namespace{}))
		}).Should(BeTrue())

		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, depKey, &appsv1.Deployment{})).To(Succeed())
		recreated := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: namespaceName}, recreated)).To(Succeed())
		Expect(recreated.Status.Phase).To(Equal(corev1.NamespaceActive))
	})
})
