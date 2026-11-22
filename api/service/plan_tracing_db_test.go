package service

import (
	"slices"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/api/reqctx"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

var planPhaseSpans = []string{planParseSpan, planMergeSpan, planLoadLiveSpan, planResolveImagesSpan, planComputeSpan}

func TestPlanAndApplyRecordASpanPerPhase(t *testing.T) {
	recorder := tracingtest.Record(t)
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := []byte(planFileHeader + applyFileWorker)
	request := tracingtest.StartParent(t)
	ctx := reqctx.WithPrincipal(request, reqctx.Principal{
		UserID: f.userID, ActorType: genDb.DeploymentActorTypeUser,
		Method: genDb.DeploymentMethodWeb, Scopes: f.applyScopes(t),
	})
	root := trace.SpanContextFromContext(request).SpanID()
	server := newPlanServer(f)

	planned, err := server.Plan(ctx, &planv1.PlanRequest{File: file, EnvironmentId: f.envID.String()})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	spans := recorder.Ended()
	for _, name := range planPhaseSpans {
		childSpan(t, spans, root, name)
	}

	if _, err = server.Apply(ctx, &planv1.ApplyRequest{
		File:          file,
		EnvironmentId: f.envID.String(),
		Revision:      planned.GetRevision(),
		Images:        planned.GetImages(),
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	spans = recorder.Ended()[len(spans):]
	replan := childSpan(t, spans, root, applyPlanSpan)
	for _, name := range planPhaseSpans {
		childSpan(t, spans, replan.SpanContext().SpanID(), name)
	}
	service := childSpan(t, spans, root, applyServiceSpan)
	if want := attribute.String(serviceNameAttr, applyWorker); !slices.Contains(service.Attributes(), want) {
		t.Fatalf("%s attributes = %v, want %v", applyServiceSpan, service.Attributes(), want)
	}
	childSpan(t, spans, service.SpanContext().SpanID(), deploymentRenderSpan)
}

func childSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, parent trace.SpanID, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name && span.Parent().SpanID() == parent {
			return span
		}
	}
	t.Fatalf("no %s span under %s", name, parent)
	return nil
}
