package controller

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Application reconcile", func() {
	It("converges without rewriting unchanged objects and cleans up on delete", func() {
		Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed())

		pullSecretName := testPullSecretName
		pullSecret := &corev1.Secret{
			Name: pullSecretName, Namespace: testNamespace,
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		Expect(k8sClient.Create(ctx, pullSecret)).To(Succeed())

		app := &locov1alpha1.Application{
			Name: "converge", Namespace: testNamespace,
			Spec: locov1alpha1.ApplicationSpec{
				Type:        testAppType,
				ResourceID:  "converge",
				WorkspaceID: "ws",
				ServiceSpec: &locov1alpha1.ServiceSpec{
					Deployment: &locov1alpha1.ServiceDeploymentSpec{
						Image: "registry.example.com/app:v1",
						Port:  8080,
						Env:   map[string]string{"B": "2", "A": "1", "C": "3"},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())

		r := &LocoResourceReconciler{
			Client:         k8sClient,
			Scheme:         k8sClient.Scheme(),
			locoNamespace:  testNamespace,
			pullSecretName: pullSecretName,
		}
		appKey := client.ObjectKeyFromObject(app)
		req := reconcile.Request{NamespacedName: appKey}
		depKey := client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}
		envSecretName := getEnvSecretName(app)
		envKey := client.ObjectKey{Namespace: depKey.Namespace, Name: envSecretName}
		imageSecretName := getImageSecretName(app)
		imageKey := client.ObjectKey{Namespace: depKey.Namespace, Name: imageSecretName}

		result, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(deployingRequeue))

		dep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.Containers[0].EnvFrom[0].SecretRef.Name).To(Equal(envSecretName))
		imageSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, imageKey, imageSecret)).To(Succeed())
		Expect(imageSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
		Expect(imageSecret.Data).To(Equal(pullSecret.Data))
		Expect(imageSecret.Labels).To(HaveKeyWithValue(labelManagedBy, managedByValue))
		sa := &corev1.ServiceAccount{}
		Expect(k8sClient.Get(ctx, depKey, sa)).To(Succeed())
		Expect(sa.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{Name: imageSecretName}))
		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		Expect(app.Status.Phase).To(Equal(phaseDeploying))
		Expect(app.Finalizers).To(ContainElement(finalizerSecretRefresher))
		depVersion := dep.ResourceVersion
		appVersion := app.ResourceVersion

		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.ResourceVersion).To(Equal(depVersion))
		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		Expect(app.ResourceVersion).To(Equal(appVersion))

		rotated := []byte(`{"auths":{"registry.example.com":{}}}`)
		pullSecret.Data = map[string][]byte{corev1.DockerConfigJsonKey: rotated}
		Expect(k8sClient.Update(ctx, pullSecret)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, imageKey, imageSecret)).To(Succeed())
		Expect(imageSecret.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, rotated))

		r.pullSecretName = ""
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, imageKey, imageSecret)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(k8sClient.Get(ctx, depKey, sa)).To(Succeed())
		Expect(sa.ImagePullSecrets).To(BeEmpty())

		app.Spec.ServiceSpec.Deployment.Env = map[string]string{"A": "changed"}
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		envSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, envKey, envSecret)).To(Succeed())
		Expect(envSecret.Data).To(HaveKeyWithValue("A", []byte("changed")))
		Expect(envSecret.Data).NotTo(HaveKey("B"))
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.Spec.Template.Annotations).To(HaveKeyWithValue(annotationEnvSecretRV, envSecret.ResourceVersion))

		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, appKey, app)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(k8sClient.Delete(ctx, pullSecret)).To(Succeed())
	})

	It("marks an invalid spec failed and still lets it be deleted", func() {
		app := &locov1alpha1.Application{
			Name:       "invalid",
			Namespace:  testNamespace,
			Finalizers: []string{finalizerSecretRefresher},
			Spec:       locov1alpha1.ApplicationSpec{Type: testAppType, ResourceID: "invalid", WorkspaceID: "ws"},
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())

		r := &LocoResourceReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		appKey := client.ObjectKeyFromObject(app)
		req := reconcile.Request{NamespacedName: appKey}

		_, err := r.Reconcile(ctx, req)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeTrue())
		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		Expect(app.Status.Phase).To(Equal(phaseFailed))

		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, appKey, app)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})
