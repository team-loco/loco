package controller

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Persisted rollout diagnostics", func() {
	It("reports failure changes and recovery without repeating replica observations", func() {
		var output bytes.Buffer
		previousLogger := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
		DeferCleanup(func() { slog.SetDefault(previousLogger) })
		app := isolationTestApplication("ws-diagnostic", "diagnostic")
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, app)).To(Succeed()) })
		r := &ApplicationReconciler{Client: k8sClient}
		before := app.DeepCopy()
		app.Status.Rollout = &loco.RolloutStatus{Problem: &loco.Problem{
			Reason: loco.ProblemHealthCheckFailing, Detail: "Health check GET :8080/ failed: connection refused",
		}}
		Expect(r.patchStatus(ctx, app, before)).To(Succeed())
		var failure map[string]any
		Expect(json.Unmarshal(bytes.TrimSpace(output.Bytes()), &failure)).To(Succeed())
		Expect(failure["resource"]).To(Equal(app.Spec.ResourceID))
		Expect(failure["reason"]).To(Equal(string(loco.ProblemHealthCheckFailing)))
		Expect(failure["detail"]).To(Equal(app.Status.Rollout.Problem.Detail))
		output.Reset()

		before = app.DeepCopy()
		app.Status.Rollout.Replicas = []loco.ReplicaStatus{{UID: "replica-a", State: loco.ReplicaStarting}}
		app.Status.Rollout.Problem.Since = new(metav1.NewTime(time.Now()))
		Expect(r.patchStatus(ctx, app, before)).To(Succeed())
		Expect(output.Len()).To(BeZero(), "replica observations repeated the same diagnostic")

		before = app.DeepCopy()
		app.Status.Rollout.Problem.Detail = "Health check GET :8080/ returned HTTP 503"
		Expect(r.patchStatus(ctx, app, before)).To(Succeed())
		var changed map[string]any
		Expect(json.Unmarshal(bytes.TrimSpace(output.Bytes()), &changed)).To(Succeed())
		Expect(changed["detail"]).To(Equal(app.Status.Rollout.Problem.Detail))
		output.Reset()

		before = app.DeepCopy()
		app.Status.Rollout.Problem = nil
		app.Status.Rollout.Available = true
		Expect(r.patchStatus(ctx, app, before)).To(Succeed())
		var recovered map[string]any
		Expect(json.Unmarshal(bytes.TrimSpace(output.Bytes()), &recovered)).To(Succeed())
		Expect(recovered["available"]).To(BeTrue())
		Expect(recovered).NotTo(HaveKey("reason"))
		Expect(recovered).NotTo(HaveKey("detail"))
		output.Reset()

		before = app.DeepCopy()
		app.Status.Rollout.Healthy = true
		Expect(r.patchStatus(ctx, app, before)).To(Succeed())
		var healthy map[string]any
		Expect(json.Unmarshal(bytes.TrimSpace(output.Bytes()), &healthy)).To(Succeed())
		Expect(healthy["healthy"]).To(BeTrue())
	})
})
