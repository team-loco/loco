package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/pgnotify"
	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

func TestWatchDeploymentTracesRechecksAndPollsAsRootsLinkedToTheStream(t *testing.T) {
	recorder := tracingtest.Record(t)
	f := newDeployFixture(t)
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	server, ctx := f.deploymentWriter(t)
	server.notifier = pgnotify.New(f.pool.Config().ConnConfig, "deployment_activity", 30*time.Second)
	principal, _ := reqctx.PrincipalFrom(ctx)
	principal.Scopes = []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
	}
	streams := make(chan trace.SpanContext, 1)
	rpcSpan := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			ctx, span := otel.Tracer(t.Name()).Start(ctx, spec.Procedure)
			defer span.End()
			streams <- span.SpanContext()
			return next(streamTestContext(ctx, principal), spec, stream)
		}
	}
	rpcServer := connect.NewServer(rpcSpan)
	deploymentv1connect.RegisterDeploymentServiceHandler(rpcServer, server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := rpc.ForHost(endpoint.URL, "").Deployment.WatchDeployment(
		streamCtx,
		&deploymentv1.WatchDeploymentRequest{DeploymentId: created.GetDeploymentId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	parent := <-streams
	for _, name := range []string{"stream.recheck", deploymentWatchPollSpan} {
		span := waitForSpan(t, recorder, name)
		if span.Parent().IsValid() || span.SpanContext().TraceID() == parent.TraceID() {
			t.Fatalf("%s continues trace %s, want its own root", name, span.SpanContext().TraceID())
		}
		if links := span.Links(); len(links) != 1 || links[0].SpanContext.SpanID() != parent.SpanID() {
			t.Fatalf("%s links = %v, want the stream span %v", name, links, parent)
		}
	}
	recheck := waitForSpan(t, recorder, "stream.recheck")
	want := semconv.RPCMethod("loco.deployment.v1.DeploymentService/WatchDeployment")
	if !slices.Contains(recheck.Attributes(), want) {
		t.Fatalf("recheck attributes = %v, want %v", recheck.Attributes(), want)
	}
}
