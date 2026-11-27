package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/team-loco/loco/controller/internal/isolation"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Environment infrastructure", func() {
	It("keeps shared policies intact without rewriting them for sibling application observations", func() {
		transport := &policyWriteTransport{}
		observedConfig := rest.CopyConfig(cfg)
		observedConfig.Wrap(func(base http.RoundTripper) http.RoundTripper { transport.base = base; return transport })
		observed, err := client.New(observedConfig, client.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())
		r := &ApplicationReconciler{
			Client:                   observed,
			APIReader:                observed,
			Scheme:                   observed.Scheme(),
			LocoNamespace:            testNamespace,
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
		}
		first := isolationTestApplication("ws-env-writes", "env-first")
		second := isolationTestApplication("ws-env-writes", "env-second")
		transport.beforeDeployment = func() error {
			namespace := testEnvironmentNamespace(first)
			for _, name := range []string{
				isolation.PolicyDefaultDeny, policyWorkspaceAccess, isolation.PolicyDNSEgress,
				policyTelemetryEgress, isolation.PolicyInternetEgress,
			} {
				policy := &networkingv1.NetworkPolicy{}
				if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, policy); err != nil {
					return fmt.Errorf("workload preceded prerequisite policy %s: %w", name, err)
				}
				if len(policy.Spec.PodSelector.MatchLabels) != 0 {
					return errors.New("environment policy does not cover every workload")
				}
				if name == isolation.PolicyDefaultDeny {
					if len(policy.Spec.Ingress) != 0 || len(policy.Spec.Egress) != 0 {
						return errors.New("default policy does not deny traffic")
					}
				}
			}
			return nil
		}
		for _, app := range []*loco.Application{first, second} {
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(app)})
			Expect(err).NotTo(HaveOccurred())
		}
		transport.reset()
		for range 3 {
			for _, app := range []*loco.Application{first, second} {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(app)})
				Expect(err).NotTo(HaveOccurred())
			}
		}
		writes := transport.count()
		fmt.Fprintf(
			GinkgoWriter,
			"synthetic environment: 2 applications, 6 unchanged observations, shared policy writes=%d\n",
			writes,
		)
		Expect(writes).To(BeZero(), "unchanged sibling observations rewrote shared environment policies")
		policy := &networkingv1.NetworkPolicy{}
		key := client.ObjectKey{Namespace: testEnvironmentNamespace(first), Name: isolation.PolicyDefaultDeny}
		Expect(k8sClient.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.PolicyTypes).To(ConsistOf(networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress))
		Expect(policy.Spec.Ingress).To(BeEmpty())
		Expect(policy.Spec.Egress).To(BeEmpty())
		for _, app := range []*loco.Application{first, second} {
			Expect(k8sClient.Delete(ctx, app)).To(Succeed())
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(app)})
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("repairs deleted and modified policies and service accounts through watches after rollout settles", func() {
		recorder := tracetest.NewSpanRecorder()
		previous := otel.GetTracerProvider()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		otel.SetTracerProvider(provider)
		DeferCleanup(func() { otel.SetTracerProvider(previous); Expect(provider.Shutdown(ctx)).To(Succeed()) })
		mgr, err := ctrl.NewManager(
			cfg,
			ctrl.Options{
				Scheme:     k8sClient.Scheme(),
				Cache:      CacheOptions(testNamespace, "", false),
				Controller: config.Controller{SkipNameValidation: new(true)},
				Metrics: metricsserver.Options{
					BindAddress: disabledListener,
				},
				HealthProbeBindAddress: disabledListener,
			},
		)
		Expect(err).NotTo(HaveOccurred())
		r := &ApplicationReconciler{
			Client:                   mgr.GetClient(),
			APIReader:                mgr.GetAPIReader(),
			Scheme:                   mgr.GetScheme(),
			LocoNamespace:            testNamespace,
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
		}
		seed := &ApplicationReconciler{
			Client:                   k8sClient,
			APIReader:                k8sClient,
			Scheme:                   k8sClient.Scheme(),
			LocoNamespace:            testNamespace,
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
		}
		app := isolationTestApplication("ws-env-watch", "env-watch")
		app.Annotations = map[string]string{loco.KeyPlacementRevision: "1"}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		key := client.ObjectKeyFromObject(app)
		_, err = seed.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		dep := &appsv1.Deployment{}
		Expect(
			k8sClient.Get(ctx, client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: testAppName(app)}, dep),
		).To(Succeed())
		dep.Status = appsv1.DeploymentStatus{
			ObservedGeneration: dep.Generation,
			Replicas:           1,
			UpdatedReplicas:    1,
			ReadyReplicas:      1,
			AvailableReplicas:  1,
		}
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
		app.Status.Rollout = &loco.RolloutStatus{Available: true, Healthy: true, ProvenRevision: 1, DeployedRevision: 1}
		Expect(k8sClient.Status().Update(ctx, app)).To(Succeed())
		result, err := seed.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero(), "fixture must have no rollout timer that can mask a missing watch")
		Expect(r.SetupWithManager(mgr)).To(Succeed())
		managerCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- mgr.Start(managerCtx) }()
		DeferCleanup(func() { stop(); Eventually(done, routeManagerWait).Should(Receive(BeNil())) })
		Expect(mgr.GetCache().WaitForCacheSync(managerCtx)).To(BeTrue())
		policyKey := client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: isolation.PolicyDefaultDeny}
		policy := &networkingv1.NetworkPolicy{}
		Eventually(
			func() error { return mgr.GetClient().Get(ctx, policyKey, policy) },
			routeManagerWait,
			routeManagerPoll,
		).Should(Succeed())
		Eventually(
			func() error { return environmentControllersIdle(recorder) },
			routeManagerWait,
			routeManagerPoll,
		).Should(Succeed())
		original := policy.UID
		Expect(k8sClient.Delete(ctx, policy)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, policyKey, policy)).To(Succeed())
			g.Expect(policy.UID).NotTo(Equal(original))
			g.Expect(policy.Spec.Ingress).To(BeEmpty())
			g.Expect(policy.Spec.Egress).To(BeEmpty())
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{}}
		Expect(k8sClient.Update(ctx, policy)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, policyKey, policy)).To(Succeed())
			g.Expect(policy.Spec.Egress).To(BeEmpty())
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		delete(policy.Labels, locolabels.LabelManagedBy)
		Expect(k8sClient.Update(ctx, policy)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, policyKey, policy)).To(Succeed())
			g.Expect(policy.Labels).To(HaveKeyWithValue(locolabels.LabelManagedBy, locolabels.ControllerManagedBy))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		account := &corev1.ServiceAccount{}
		accountKey := client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: testAppName(app)}
		Expect(k8sClient.Get(ctx, accountKey, account)).To(Succeed())
		accountUID := account.UID
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, accountKey, account)).To(Succeed())
			g.Expect(account.UID).NotTo(Equal(accountUID))
		}, routeManagerWait, routeManagerPoll).Should(Succeed())
		fmt.Fprintf(
			GinkgoWriter,
			"synthetic settled rollout: policy deletion and drift repaired; "+
				"service account recreated without a rollout timer\n",
		)
		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
	})
	It("does not create a workload when a prerequisite policy write fails", func() {
		transport := &policyWriteTransport{failPolicyCreate: true}
		observedConfig := rest.CopyConfig(cfg)
		observedConfig.Wrap(func(base http.RoundTripper) http.RoundTripper { transport.base = base; return transport })
		observed, err := client.New(observedConfig, client.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())
		r := &ApplicationReconciler{
			Client:                   observed,
			APIReader:                observed,
			Scheme:                   observed.Scheme(),
			LocoNamespace:            testNamespace,
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry:                testTelemetry(),
		}
		app := isolationTestApplication("ws-policy-failure", "policy-failure")
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(err).To(MatchError(ContainSubstring("injected policy transport failure")))
		deployment := &appsv1.Deployment{}
		err = k8sClient.Get(
			ctx,
			client.ObjectKey{Namespace: testEnvironmentNamespace(app), Name: testAppName(app)},
			deployment,
		)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "workload was created despite failed isolation setup")
		transport.failPolicyCreate = false
		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
		_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(app)})
		Expect(err).NotTo(HaveOccurred())
	})
})

type policyWriteTransport struct {
	base             http.RoundTripper
	mu               sync.Mutex
	writes           int
	beforeDeployment func() error
	failPolicyCreate bool
}

func (t *policyWriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && strings.Contains(req.URL.Path, "/networkpolicies") {
		if t.failPolicyCreate {
			return nil, errors.New("injected policy transport failure")
		}
	}
	if req.Method == http.MethodPatch && strings.Contains(req.URL.Path, "/deployments/") {
		if t.beforeDeployment != nil {
			if err := t.beforeDeployment(); err != nil {
				return nil, err
			}
		}
	}
	shared := false
	switch path.Base(req.URL.Path) {
	case isolation.PolicyDefaultDeny,
		policyWorkspaceAccess,
		isolation.PolicyDNSEgress,
		policyTelemetryEgress,
		isolation.PolicyInternetEgress:
		shared = true
	}
	if shared && req.Method != http.MethodGet {
		t.mu.Lock()
		t.writes++
		t.mu.Unlock()
	}
	return t.base.RoundTrip(req)
}
func (t *policyWriteTransport) reset()     { t.mu.Lock(); defer t.mu.Unlock(); t.writes = 0 }
func (t *policyWriteTransport) count() int { t.mu.Lock(); defer t.mu.Unlock(); return t.writes }

func environmentControllersIdle(recorder *tracetest.SpanRecorder) error {
	seen := make(map[string]bool)
	for _, span := range recorder.Ended() {
		seen[span.Name()] = true
	}
	if !seen[reconcileSpan] {
		return errors.New("initial reconciliations have not completed")
	}
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		return err
	}
	var gauges int
	for _, family := range families {
		switch family.GetName() {
		case "controller_runtime_active_workers", "workqueue_depth":
		default:
			continue
		}
		for _, metric := range family.GetMetric() {
			relevant := false
			for _, label := range metric.GetLabel() {
				switch label.GetValue() {
				case "application", "environment":
					relevant = true
				}
			}
			if !relevant {
				continue
			}
			gauges++
			if metric.GetGauge().GetValue() != 0 {
				return fmt.Errorf("controller work remains: %s", family.GetName())
			}
		}
	}
	if gauges == 0 {
		return errors.New("controller queue metrics are missing")
	}
	return nil
}
