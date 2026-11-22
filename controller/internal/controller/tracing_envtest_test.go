package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/team-loco/loco/controller/internal/tracing"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Application reconcile tracing", func() {
	It("records the reconcile, its steps and their API calls, failing only the step that failed", func() {
		recorder := tracetest.NewSpanRecorder()
		previous := otel.GetTracerProvider()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
		DeferCleanup(func() { otel.SetTracerProvider(previous) })

		tracedConfig := rest.CopyConfig(cfg)
		tracing.TraceRequests(tracedConfig)
		tracedClient, err := client.New(tracedConfig, client.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())

		app := isolationTestApplication("ws-traced", "traced")
		app.Annotations = map[string]string{
			locov1alpha1.KeyPlacementID: "p-traced",
			locov1alpha1.KeyTraceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		}
		app.Spec.ServiceSpec.Deployment.EnvSecretRef = &locov1alpha1.EnvSecretRef{Name: "env-traced", Revision: 1}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		staged := &corev1.Secret{
			Name: "env-traced", Namespace: testNamespace,
			Labels: map[string]string{
				locov1alpha1.KeyPlacementID:       "p-other",
				locov1alpha1.KeyPlacementRevision: "1",
			},
		}
		r := &ApplicationReconciler{
			Client:                   tracedClient,
			APIReader:                tracedClient,
			Scheme:                   k8sClient.Scheme(),
			Telemetry:                testTelemetry(),
			Gateway:                  testGateway,
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		}
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(app)}
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, staged)).To(Succeed())
			Expect(k8sClient.Delete(ctx, app)).To(Succeed())
			_, deleteErr := r.Reconcile(ctx, req)
			Expect(deleteErr).NotTo(HaveOccurred())
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, req.NamespacedName, app))).To(BeTrue())
		})

		result, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(envSecretRequeue))
		waiting := recorder.Ended()
		Expect(endedSpan(waiting, reconcileSpan).Status().Code).To(Equal(codes.Unset))
		waitingStep := endedSpan(waiting, envSecretSpan)
		Expect(waitingStep.Status().Code).To(Equal(codes.Unset))
		Expect(waitingStep.Attributes()).To(ContainElement(attribute.Bool("loco.reconcile.waiting", true)))

		Expect(k8sClient.Create(ctx, staged)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).To(MatchError(errEnvSecretPlacement))

		spans := recorder.Ended()[len(waiting):]
		root := endedSpan(spans, reconcileSpan)
		Expect(root.Status().Code).To(Equal(codes.Error))
		Expect(root.Parent().IsValid()).To(BeFalse())
		Expect(root.Links()).To(HaveLen(1))
		Expect(root.Links()[0].SpanContext.TraceID().String()).To(Equal("4bf92f3577b34da6a3ce929d0e0e4736"))
		Expect(root.Links()[0].SpanContext.SpanID().String()).To(Equal("00f067aa0ba902b7"))
		Expect(root.Attributes()).To(ContainElements(
			attribute.String(placementIDAttr, "p-traced"),
			attribute.String(resourceIDAttr, "traced"),
		))
		children := map[string]sdktrace.ReadOnlySpan{}
		for _, span := range spans {
			if span.Parent().SpanID() == root.SpanContext().SpanID() && span.SpanKind() == trace.SpanKindInternal {
				children[span.Name()] = span
			}
		}
		Expect(children).To(HaveKey(finalizerSpan))
		Expect(children).To(HaveKey(namespaceSpan))
		Expect(children).To(HaveKey(statusSpan))
		Expect(children).NotTo(HaveKey(deploymentSpan))
		Expect(children).To(HaveKey(envSecretSpan))
		Expect(children[envSecretSpan].Status().Code).To(Equal(codes.Error))
		Expect(children[namespaceSpan].Status().Code).To(Equal(codes.Unset))
		Expect(spans).To(ContainElement(Satisfy(func(span sdktrace.ReadOnlySpan) bool {
			return span.SpanKind() == trace.SpanKindClient &&
				span.Parent().SpanID() == children[namespaceSpan].SpanContext().SpanID()
		})), "no Kubernetes API call is a child of the namespace step")
	})
})

func endedSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	Fail("no " + name + " span")
	return nil
}
