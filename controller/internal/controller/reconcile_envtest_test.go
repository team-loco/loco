package controller

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/team-loco/loco/controller/internal/managed"
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
						Image:        "registry.example.com/app:v1",
						Port:         8080,
						Env:          map[string]string{"B": "2", "A": "1", "C": "3"},
						EnvSecretRef: &locov1alpha1.EnvSecretRef{Name: "env-converge", Revision: 2},
					},
					Resources: testResources(),
				},
			},
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		podRole := &rbacv1.Role{Name: getRoleName(app), Namespace: getNamespace(app)}
		podNamespace := &corev1.Namespace{Name: getNamespace(app)}
		Expect(k8sClient.Create(ctx, podNamespace)).To(Succeed())
		Expect(k8sClient.Create(ctx, podRole)).To(Succeed())

		r := &LocoResourceReconciler{
			Client:         k8sClient,
			Scheme:         k8sClient.Scheme(),
			LocoNamespace:  testNamespace,
			PullSecretName: pullSecretName,
		}
		appKey := client.ObjectKeyFromObject(app)
		req := reconcile.Request{NamespacedName: appKey}
		depKey := client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}
		envSecretName := getEnvSecretName(app)
		envKey := client.ObjectKey{Namespace: depKey.Namespace, Name: envSecretName}
		imageKey := client.ObjectKey{Namespace: depKey.Namespace, Name: workspacePullSecretName}

		result, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(envSecretRequeue))
		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		Expect(app.Status.Phase).To(Equal(phaseDeploying))
		dep := &appsv1.Deployment{}
		err = k8sClient.Get(ctx, depKey, dep)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "a Deployment was created before its secrets arrived")

		staged := &corev1.Secret{
			Name: "env-converge", Namespace: testNamespace,
			Labels: map[string]string{locov1alpha1.LabelPlacementRevision: "1"},
			Data:   map[string][]byte{"DATABASE_URL": []byte("postgres://old")},
		}
		Expect(k8sClient.Create(ctx, staged)).To(Succeed())
		result, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(envSecretRequeue))

		staged.Labels[locov1alpha1.LabelPlacementRevision] = "2"
		staged.Data["DATABASE_URL"] = []byte("postgres://new")
		Expect(k8sClient.Update(ctx, staged)).To(Succeed())
		result, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(deployingRequeue))

		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		container := dep.Spec.Template.Spec.Containers[0]
		Expect(container.EnvFrom[0].SecretRef.Name).To(Equal(envSecretName))
		Expect(container.Env[0]).To(Equal(corev1.EnvVar{Name: "A", Value: "1"}))
		Expect(dep.Spec.Template.Annotations).To(HaveKeyWithValue(annotationEnvSecretRevision, "2"))
		envSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, envKey, envSecret)).To(Succeed())
		Expect(envSecret.Data).To(Equal(map[string][]byte{"DATABASE_URL": []byte("postgres://new")}))
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(podRole), podRole)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the pod's Secret-reading Role survived")
		imageSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, imageKey, imageSecret)).To(Succeed())
		Expect(imageSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
		Expect(imageSecret.Data).To(Equal(pullSecret.Data))
		Expect(imageSecret.Labels).To(HaveKeyWithValue(managed.LabelManagedBy, managed.ManagedByValue))
		Expect(imageSecret.Labels).To(HaveKeyWithValue(managed.LabelWorkspaceID, app.Spec.WorkspaceID))
		Expect(imageSecret.Annotations).NotTo(HaveKey(annotationAppName))
		sa := &corev1.ServiceAccount{}
		Expect(k8sClient.Get(ctx, depKey, sa)).To(Succeed())
		Expect(sa.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{Name: workspacePullSecretName}))
		Expect(k8sClient.Get(ctx, depKey, &corev1.Service{})).To(Succeed())
		gatewayPolicyKey := client.ObjectKey{Namespace: depKey.Namespace, Name: getGatewayPolicyName(app)}
		err = k8sClient.Get(ctx, gatewayPolicyKey, &networkingv1.NetworkPolicy{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		Expect(app.Status.Phase).To(Equal(phaseDeploying))
		Expect(app.Finalizers).To(ConsistOf(finalizerAppResourcesCleanup))
		depVersion := dep.ResourceVersion
		appVersion := app.ResourceVersion

		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.ResourceVersion).To(Equal(depVersion))
		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		Expect(app.ResourceVersion).To(Equal(appVersion))

		staged.Labels[locov1alpha1.LabelPlacementRevision] = "3"
		staged.Data["DATABASE_URL"] = []byte("postgres://next")
		Expect(k8sClient.Update(ctx, staged)).To(Succeed())
		result, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(envSecretRequeue))
		Expect(k8sClient.Get(ctx, envKey, envSecret)).To(Succeed())
		Expect(envSecret.Data).To(HaveKeyWithValue("DATABASE_URL", []byte("postgres://new")),
			"the env Secret was copied ahead of the Application's reference")
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.Spec.Template.Annotations).To(HaveKeyWithValue(annotationEnvSecretRevision, "2"))

		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		app.Spec.ServiceSpec.Deployment.EnvSecretRef.Revision = 3
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, envKey, envSecret)).To(Succeed())
		Expect(envSecret.Data).To(HaveKeyWithValue("DATABASE_URL", []byte("postgres://next")))
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.Spec.Template.Annotations).To(HaveKeyWithValue(annotationEnvSecretRevision, "3"))

		rotated := []byte(`{"auths":{"registry.example.com":{}}}`)
		pullSecret.Data = map[string][]byte{corev1.DockerConfigJsonKey: rotated}
		Expect(k8sClient.Update(ctx, pullSecret)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, imageKey, imageSecret)).To(Succeed())
		Expect(imageSecret.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, rotated))

		r.PullSecretName = ""
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, imageKey, imageSecret)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(k8sClient.Get(ctx, depKey, sa)).To(Succeed())
		Expect(sa.ImagePullSecrets).To(BeEmpty())

		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		app.Spec.ServiceSpec.Deployment.EnvSecretRef = nil
		Expect(k8sClient.Update(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, envKey, envSecret)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the env Secret outlived its reference")
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.Containers[0].EnvFrom).To(BeEmpty())
		Expect(dep.Spec.Template.Annotations).NotTo(HaveKey(annotationEnvSecretRevision))

		Expect(k8sClient.Delete(ctx, staged)).To(Succeed())
		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, appKey, app)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(k8sClient.Delete(ctx, pullSecret)).To(Succeed())
	})

	It("shares one pull secret across the apps of a workspace", func() {
		Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed())

		pullSecret := &corev1.Secret{
			Name: "shared-registry-pull", Namespace: testNamespace,
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		Expect(k8sClient.Create(ctx, pullSecret)).To(Succeed())

		first := isolationTestApplication("ws-shared-pull", "shared-pull-first")
		second := isolationTestApplication("ws-shared-pull", "shared-pull-second")
		first.Spec.ServiceSpec.Routing = nil
		second.Spec.ServiceSpec.Routing = nil
		Expect(k8sClient.Create(ctx, first)).To(Succeed())
		Expect(k8sClient.Create(ctx, second)).To(Succeed())

		r := &LocoResourceReconciler{
			Client:         k8sClient,
			Scheme:         k8sClient.Scheme(),
			LocoNamespace:  testNamespace,
			PullSecretName: pullSecret.Name,
		}
		firstKey := client.ObjectKeyFromObject(first)
		secondKey := client.ObjectKeyFromObject(second)
		firstReq := reconcile.Request{NamespacedName: firstKey}
		secondReq := reconcile.Request{NamespacedName: secondKey}
		namespace := getNamespace(first)
		secretKey := client.ObjectKey{Namespace: namespace, Name: workspacePullSecretName}
		firstName := getName(first)
		secondName := getName(second)
		firstSAKey := client.ObjectKey{Namespace: namespace, Name: firstName}
		secondSAKey := client.ObjectKey{Namespace: namespace, Name: secondName}
		reference := corev1.LocalObjectReference{Name: workspacePullSecretName}

		_, err := r.Reconcile(ctx, firstReq)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.Reconcile(ctx, secondReq)
		Expect(err).NotTo(HaveOccurred())

		secrets := &corev1.SecretList{}
		Expect(k8sClient.List(ctx, secrets, client.InNamespace(namespace))).To(Succeed())
		var pullSecrets []string
		for _, secret := range secrets.Items {
			if secret.Type == corev1.SecretTypeDockerConfigJson {
				pullSecrets = append(pullSecrets, secret.Name)
			}
		}
		Expect(pullSecrets).To(ConsistOf(workspacePullSecretName))

		sa := &corev1.ServiceAccount{}
		Expect(k8sClient.Get(ctx, firstSAKey, sa)).To(Succeed())
		Expect(sa.ImagePullSecrets).To(ConsistOf(reference))
		Expect(k8sClient.Get(ctx, secondSAKey, sa)).To(Succeed())
		Expect(sa.ImagePullSecrets).To(ConsistOf(reference))

		rotated := []byte(`{"auths":{"registry.example.com":{}}}`)
		pullSecret.Data = map[string][]byte{corev1.DockerConfigJsonKey: rotated}
		Expect(k8sClient.Update(ctx, pullSecret)).To(Succeed())
		requests := r.applicationPerWorkspace(ctx, pullSecret)
		var workspaceRequests []reconcile.Request
		for _, req := range requests {
			if req == firstReq || req == secondReq {
				workspaceRequests = append(workspaceRequests, req)
			}
		}
		Expect(workspaceRequests).To(HaveLen(1))
		_, err = r.Reconcile(ctx, workspaceRequests[0])
		Expect(err).NotTo(HaveOccurred())
		workspaceSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, secretKey, workspaceSecret)).To(Succeed())
		Expect(workspaceSecret.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, rotated))

		Expect(k8sClient.Delete(ctx, first)).To(Succeed())
		_, err = r.Reconcile(ctx, firstReq)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, firstKey, first)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(k8sClient.Get(ctx, secretKey, workspaceSecret)).To(Succeed())
		Expect(k8sClient.Get(ctx, secondSAKey, sa)).To(Succeed())
		Expect(sa.ImagePullSecrets).To(ConsistOf(reference))

		r.PullSecretName = ""
		_, err = r.Reconcile(ctx, secondReq)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, secretKey, workspaceSecret)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(k8sClient.Get(ctx, secondSAKey, sa)).To(Succeed())
		Expect(sa.ImagePullSecrets).To(BeEmpty())

		Expect(k8sClient.Delete(ctx, second)).To(Succeed())
		_, err = r.Reconcile(ctx, secondReq)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Delete(ctx, pullSecret)).To(Succeed())
	})

	It("lets a system variable win over a plain env entry of the same name", func() {
		Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed())

		app := isolationTestApplication("ws-system-env", "system-env")
		app.Spec.ServiceSpec.Routing = nil
		app.Spec.ServiceSpec.Deployment.Env = map[string]string{envRegion: "user-value", "A": "1"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())

		r := &LocoResourceReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), LocoNamespace: testNamespace}
		appKey := client.ObjectKeyFromObject(app)
		req := reconcile.Request{NamespacedName: appKey}
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		dep := &appsv1.Deployment{}
		depKey := client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}
		Expect(k8sClient.Get(ctx, depKey, dep)).To(Succeed())
		var regions []corev1.EnvVar
		for _, envVar := range dep.Spec.Template.Spec.Containers[0].Env {
			if envVar.Name == envRegion {
				regions = append(regions, envVar)
			}
		}
		Expect(regions).To(ConsistOf(corev1.EnvVar{Name: envRegion, Value: app.Spec.Region}))
		Expect(dep.Spec.Template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "A", Value: "1"}))

		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
	})

	It("marks an invalid spec failed and still lets it be deleted", func() {
		app := &locov1alpha1.Application{
			Name:       "invalid",
			Namespace:  testNamespace,
			Finalizers: []string{finalizerAppResourcesCleanup},
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
	It("rejects an Application without the values the controller requires", func() {
		valid := isolationTestApplication("ws-required", "required-valid")
		Expect(k8sClient.Create(ctx, valid)).To(Succeed())
		Expect(k8sClient.Delete(ctx, valid)).To(Succeed())

		missing := map[string]func(*locov1alpha1.ServiceSpec){
			"resources":       func(spec *locov1alpha1.ServiceSpec) { spec.Resources = nil },
			"cpu":             func(spec *locov1alpha1.ServiceSpec) { spec.Resources.CPU = "" },
			"memory":          func(spec *locov1alpha1.ServiceSpec) { spec.Resources.Memory = "" },
			"min replicas":    func(spec *locov1alpha1.ServiceSpec) { spec.Resources.Replicas.Min = 0 },
			"max replicas":    func(spec *locov1alpha1.ServiceSpec) { spec.Resources.Replicas.Max = 0 },
			"port":            func(spec *locov1alpha1.ServiceSpec) { spec.Deployment.Port = 0 },
			"path prefix":     func(spec *locov1alpha1.ServiceSpec) { spec.Routing.PathPrefix = "" },
			"idle timeout":    func(spec *locov1alpha1.ServiceSpec) { spec.Routing.IdleTimeout = 0 },
			"health interval": func(spec *locov1alpha1.ServiceSpec) { spec.Deployment.HealthCheck.Interval = 0 },
		}
		for name, unset := range missing {
			app := isolationTestApplication("ws-required", "required-missing")
			app.Spec.ServiceSpec.Deployment.HealthCheck = &locov1alpha1.HealthCheckSpec{
				Path: "/health", Interval: 5, Timeout: 2, FailThreshold: 3,
			}
			unset(app.Spec.ServiceSpec)
			err := k8sClient.Create(ctx, app)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "creating an Application without %s returned %v", name, err)
		}
	})
})
