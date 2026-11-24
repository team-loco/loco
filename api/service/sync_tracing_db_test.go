package service

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/pkg/tracing"
	"github.com/team-loco/loco/api/pkg/tracingtest"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

func waitForSpan(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, span := range recorder.Ended() {
			if span.Name() == name {
				return span
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no ended %s span", name)
	return nil
}

func TestSyncSpansStartTheirOwnTracesLinkedToTheDeployRequest(t *testing.T) {
	recorder := tracingtest.Record(t)
	f := newDeployFixture(t)
	client := startSyncServer(t, f)
	request := tracingtest.StartParent(t)
	if _, err := f.deploy(request, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	stream := openSync(t.Context(), t, client, f.clusterID)
	got := nextResult(t, stream)
	if got.err != nil || got.msg.GetApply() == nil {
		t.Fatalf("stream got %v, %v; want an Apply", got.msg, got.err)
	}
	if want := tracing.Traceparent(request); got.msg.GetApply().GetTraceparent() != want {
		t.Errorf("Apply traceparent = %q, want the deploy request's %q", got.msg.GetApply().GetTraceparent(), want)
	}
	placement := f.placement(t, f.clusterID)
	session := newSyncSession(f.agentServer(), f.clusterID, func(*agentv1.SyncResponse) error { return nil })
	for _, msg := range []*agentv1.SyncRequest{
		{Message: &agentv1.SyncRequest_Applied{Applied: applied(placement.ID, placement.DesiredRevision)}},
		{Message: &agentv1.SyncRequest_Status{Status: &agentv1.PlacementStatus{
			PlacementId: placement.ID.String(), ObservedRevision: placement.DesiredRevision, ReadyReplicas: 1,
		}}},
	} {
		if err := session.handle(t.Context(), msg); err != nil {
			t.Fatalf("handle %T: %v", msg.GetMessage(), err)
		}
	}
	if err := applyQueuedReports(t.Context(), session); err != nil {
		t.Fatalf("apply reports: %v", err)
	}

	origin := trace.SpanContextFromContext(request)
	for _, name := range []string{syncApplySpan, syncAppliedSpan, syncStatusSpan} {
		span := waitForSpan(t, recorder, name)
		if span.Parent().IsValid() || span.SpanContext().TraceID() == origin.TraceID() {
			t.Errorf("%s continues the deploy request's trace instead of starting its own", name)
		}
		links := span.Links()
		if len(links) != 1 || links[0].SpanContext.SpanID() != origin.SpanID() ||
			links[0].SpanContext.TraceID() != origin.TraceID() {
			t.Errorf("%s links = %v, want the deploy request's span %s", name, links, origin.SpanID())
		}
		placementID := ""
		for _, attr := range span.Attributes() {
			if attr.Key == syncPlacementIDAttr {
				placementID = attr.Value.AsString()
			}
		}
		if placementID != placement.ID.String() {
			t.Errorf("%s placement id = %q, want %s", name, placementID, placement.ID)
		}
	}
	ended := len(recorder.Ended())
	heartbeat := &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{Heartbeat: &agentv1.Heartbeat{}}}
	if err := session.handle(t.Context(), heartbeat); err != nil {
		t.Fatalf("handle heartbeat: %v", err)
	}
	if got := len(recorder.Ended()); got != ended {
		t.Errorf("a heartbeat ended %d spans, want none", got-ended)
	}
}

func TestBuildStatusSpanLinksToTheRequestThatCreatedTheBuild(t *testing.T) {
	recorder := tracingtest.Record(t)
	f := newBuildFixture(t)
	request, span := otel.Tracer(t.Name()).Start(f.ctx, "request")
	created, err := f.server.CreateBuild(request, &buildv1.CreateBuildRequest{
		ResourceId: f.resourceID.String(), DockerfilePath: testDockerfile, SourceSize: 10,
	})
	span.End()
	if err != nil {
		t.Fatalf("create build: %v", err)
	}
	session, _ := f.dispatchSession()
	status := buildStatus(created.GetBuildId(), agentv1.BuildPhase_BUILD_PHASE_RUNNING)
	if err := session.handleNow(t.Context(), &agentv1.SyncRequest{
		Message: &agentv1.SyncRequest_BuildStatus{BuildStatus: status},
	}); err != nil {
		t.Fatalf("handle build status: %v", err)
	}
	links := waitForSpan(t, recorder, syncBuildStatusSpan).Links()
	if len(links) != 1 || links[0].SpanContext.SpanID() != span.SpanContext().SpanID() {
		t.Fatalf("build status links = %v, want the create request's span %s", links, span.SpanContext().SpanID())
	}
}

func TestQueuedBuildIsSentWithTheTraceOfTheRequestThatCreatedIt(t *testing.T) {
	tracingtest.Record(t)
	f := newBuildFixture(t)
	request, span := otel.Tracer(t.Name()).Start(f.ctx, "request")
	defer span.End()
	created, err := f.server.CreateBuild(request, &buildv1.CreateBuildRequest{
		ResourceId: f.resourceID.String(), DockerfilePath: testDockerfile, SourceSize: 10,
	})
	if err != nil {
		t.Fatalf("create build: %v", err)
	}
	f.uploadFor(t, created.GetBuildId(), 10)
	if _, err := f.start(created.GetBuildId()); err != nil {
		t.Fatalf("start build: %v", err)
	}
	session, sent := f.dispatchSession()
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	starts := startedBuilds(sent.take())
	if want := tracing.Traceparent(request); len(starts) != 1 || starts[0].GetTraceparent() != want {
		t.Fatalf("sent %v, want one StartBuild with the create request's traceparent %q", starts, want)
	}
}
