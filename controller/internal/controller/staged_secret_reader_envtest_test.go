package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const stagedCacheTimeout = 10 * time.Second

var _ = Describe("Staged environment secret reads", func() {
	It("reads staging revisions excluded by the production cache", func() {
		app := workspaceApp("stage-reader", "stage-reader")
		app.Spec.WorkspaceID = "staged-reader-cache"
		app.Namespace = testNamespace
		stage := &corev1.Secret{
			Name:      "env-staged-reader",
			Namespace: testNamespace,
			Labels: map[string]string{
				locov1alpha1.KeyPlacementID:       testStagedPlacementID,
				locov1alpha1.KeyPlacementRevision: "1",
			},
			Data: map[string][]byte{"VALUE": []byte("initial"), envOTelTracesSamplerArg: []byte("0")},
		}
		Expect(k8sClient.Create(ctx, stage)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, stage)).To(Succeed()) })
		namespace := &corev1.Namespace{Name: testWorkspaceNamespace(app)}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementID: testStagedPlacementID}
		app.Spec.ServiceSpec.Deployment.EnvSecretRef = &locov1alpha1.EnvSecretRef{Name: stage.Name, Revision: 1}
		filtered, err := cache.New(cfg, CacheOptions(testNamespace, "registry-filtered-reader", false))
		Expect(err).NotTo(HaveOccurred())
		cacheCtx, stop := context.WithCancel(ctx)
		stopped := make(chan error, 1)
		go func() { stopped <- filtered.Start(cacheCtx) }()
		DeferCleanup(func() { stop(); Expect(<-stopped).To(Succeed()) })
		key := client.ObjectKeyFromObject(stage)
		Eventually(func() bool {
			return apierrors.IsNotFound(filtered.Get(ctx, key, &corev1.Secret{}))
		}, stagedCacheTimeout).Should(BeTrue())
		cached, err := client.New(
			cfg,
			client.Options{Scheme: k8sClient.Scheme(), Cache: &client.CacheOptions{Reader: filtered}},
		)
		Expect(err).NotTo(HaveOccurred())
		r := &ApplicationReconciler{
			Client:                   cached,
			APIReader:                k8sClient,
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		}
		mount, err := r.ensureEnvSecret(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(mount.Mounted).To(BeTrue())
		Expect(mount.Revision).To(Equal(int64(1)))
		deployment, err := desiredDeployment(
			app,
			testWorkspaceNamespace(app),
			mount,
			testTelemetry(),
			testMinReadySeconds,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(deployment.Spec.Template.Spec.Containers[0].Env).NotTo(
			ContainElement(HaveField("Name", new(envOTelTracesSamplerArg))),
		)
		target := &corev1.Secret{}
		targetKey := client.ObjectKey{Namespace: namespace.Name, Name: getEnvSecretName(app)}
		Expect(k8sClient.Get(ctx, targetKey, target)).To(Succeed())
		Expect(target.Data).To(Equal(stage.Data))
		stage.Labels[locov1alpha1.KeyPlacementRevision] = "2"
		stage.Data["VALUE"] = []byte("rotated")
		stage.Data[envOTelTracesSamplerArg] = []byte("1")
		Expect(k8sClient.Update(ctx, stage)).To(Succeed())
		app.Spec.ServiceSpec.Deployment.EnvSecretRef.Revision = 2
		mount, err = r.ensureEnvSecret(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(mount.Revision).To(Equal(int64(2)))
		Expect(k8sClient.Get(ctx, targetKey, target)).To(Succeed())
		Expect(target.Data).To(Equal(stage.Data))
		deployment, err = desiredDeployment(
			app,
			testWorkspaceNamespace(app),
			mount,
			testTelemetry(),
			testMinReadySeconds,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(deployment.Spec.Template.Spec.Containers[0].Env).NotTo(
			ContainElement(HaveField("Name", new(envOTelTracesSamplerArg))),
		)

		delete(stage.Data, envOTelTracesSamplerArg)
		stage.Labels[locov1alpha1.KeyPlacementRevision] = "3"
		Expect(k8sClient.Update(ctx, stage)).To(Succeed())
		app.Spec.ServiceSpec.Deployment.EnvSecretRef.Revision = 3
		mount, err = r.ensureEnvSecret(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		deployment, err = desiredDeployment(
			app,
			testWorkspaceNamespace(app),
			mount,
			testTelemetry(),
			testMinReadySeconds,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(deployment.Spec.Template.Spec.Containers[0].Env).NotTo(
			ContainElement(HaveField("Name", new(envOTelTracesSamplerArg))),
		)
	})

	It("caches only the agent's registry pull secrets in the loco namespace in token mode", func() {
		agentSecret := &corev1.Secret{
			Name: locov1alpha1.RegistryPullSecretName("cache-token"), Namespace: testNamespace,
			Labels: map[string]string{locov1alpha1.LabelRegistryPull: "cache-token"},
			Type:   corev1.SecretTypeDockerConfigJson,
			Data:   map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		other := &corev1.Secret{Name: "cache-token-other", Namespace: testNamespace}
		for _, secret := range []*corev1.Secret{agentSecret, other} {
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, secret)).To(Succeed()) })
		}
		filtered, err := cache.New(cfg, CacheOptions(testNamespace, "", true))
		Expect(err).NotTo(HaveOccurred())
		cacheCtx, stop := context.WithCancel(ctx)
		stopped := make(chan error, 1)
		go func() { stopped <- filtered.Start(cacheCtx) }()
		DeferCleanup(func() { stop(); Expect(<-stopped).To(Succeed()) })

		agentKey := client.ObjectKeyFromObject(agentSecret)
		Eventually(func() error {
			return filtered.Get(ctx, agentKey, &corev1.Secret{})
		}, stagedCacheTimeout).Should(Succeed())
		otherKey := client.ObjectKeyFromObject(other)
		Expect(apierrors.IsNotFound(filtered.Get(ctx, otherKey, &corev1.Secret{}))).To(BeTrue())
	})
})
