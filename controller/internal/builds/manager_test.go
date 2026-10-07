package builds

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	managerBuildNamespace   = "builds-manager"
	managerOutsideNamespace = "builds-outside"
	managerPushSecret       = "registry-builder-manager"
	managerWait             = 30 * time.Second
	managerPoll             = 200 * time.Millisecond
	disabledMetrics         = "0"
)

var _ = Describe("Build controller manager", func() {
	It("runs Builds as Jobs on its own, with a cache scoped to the builds namespace", func() {
		namespace := &corev1.Namespace{Name: managerBuildNamespace}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		push := dockerConfigSecret(managerPushSecret, `{"auths":{"registry.example.com":{"auth":"cHVzaA=="}}}`)
		Expect(k8sClient.Create(ctx, push)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, push)).To(Succeed())
		})

		buildConfig := chartDefaults()
		buildConfig.Namespace = managerBuildNamespace
		buildConfig.PushSecretName = managerPushSecret
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 scheme.Scheme,
			Cache:                  CacheOptions(managerBuildNamespace),
			Metrics:                metricsserver.Options{BindAddress: disabledMetrics},
			HealthProbeBindAddress: disabledMetrics,
		})
		Expect(err).NotTo(HaveOccurred())
		reconciler := &Reconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			APIReader:     mgr.GetAPIReader(),
			Config:        buildConfig,
			LocoNamespace: testNamespace,
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		managerCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- mgr.Start(managerCtx)
		}()
		DeferCleanup(func() {
			stop()
			Eventually(done, managerWait).Should(Receive(BeNil()))
		})

		build := testBuild("managed")
		build.Namespace = managerBuildNamespace
		Expect(k8sClient.Create(ctx, build)).To(Succeed())

		key := client.ObjectKeyFromObject(build)
		Eventually(func() error {
			return k8sClient.Get(ctx, key, &batchv1.Job{})
		}, managerWait, managerPoll).Should(Succeed())
		Eventually(func() string {
			current := &locov1alpha1.Build{}
			if getErr := k8sClient.Get(ctx, key, current); getErr != nil {
				return getErr.Error()
			}
			return current.Status.Phase
		}, managerWait, managerPoll).Should(Equal(locov1alpha1.BuildPhasePending))

		outsideNamespace := &corev1.Namespace{Name: managerOutsideNamespace}
		Expect(k8sClient.Create(ctx, outsideNamespace)).To(Succeed())
		outside := testBuild("outside")
		outside.Namespace = managerOutsideNamespace
		Expect(k8sClient.Create(ctx, outside)).To(Succeed())
		outsideKey := client.ObjectKeyFromObject(outside)
		Consistently(func() string {
			current := &locov1alpha1.Build{}
			Expect(k8sClient.Get(ctx, outsideKey, current)).To(Succeed())
			return current.Status.Phase
		}, 2*time.Second, managerPoll).Should(BeEmpty())
		Expect(k8sClient.Delete(ctx, outside)).To(Succeed())
	})
})
