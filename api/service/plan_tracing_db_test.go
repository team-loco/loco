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
	wantRenderedWorker(t, spans, root)
}

func TestApplyRecordsASpanPerServiceItDeploysFromABuild(t *testing.T) {
	recorder := tracingtest.Record(t)
	f := newDeployFixture(t)
	file := createSourceWorker(t, f)
	first := f.addSucceededBuild(t, applyWorker, applyBuildDockerfile, defaultBuildContext)
	if _, err := applyBuilds(t, f, file, map[string]string{applyWorker: first.String()}); err != nil {
		t.Fatalf("Apply with the first build: %v", err)
	}
	next := f.addSucceededBuild(t, applyWorker, applyBuildDockerfile, defaultBuildContext)
	f.finishRollouts(t)
	planned, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	request := tracingtest.StartParent(t)
	ctx := reqctx.WithPrincipal(request, reqctx.Principal{
		ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb, Scopes: f.adminScopes(t),
	})

	resp, err := newPlanServer(f).Apply(ctx, &planv1.ApplyRequest{
		File:          []byte(file),
		EnvironmentId: f.envID.String(),
		Revision:      planned.GetRevision(),
		Builds:        map[string]string{applyWorker: next.String()},
		Images:        planned.GetImages(),
	})
	if err != nil {
		t.Fatalf("Apply with the next build: %v", err)
	}
	if len(resp.GetOperations()) != 0 || len(resp.GetDeployments()) != 1 {
		t.Fatalf("response = %v, want no operations and one deployment", resp)
	}
	wantRenderedWorker(t, recorder.Ended(), trace.SpanContextFromContext(request).SpanID())
}

// wantRenderedWorker fails unless parent has an apply.service span for the worker with a render under it.
func wantRenderedWorker(t *testing.T, spans []sdktrace.ReadOnlySpan, parent trace.SpanID) {
	t.Helper()
	service := childSpan(t, spans, parent, applyServiceSpan)
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
