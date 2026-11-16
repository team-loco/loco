package telemetry

import (
	"fmt"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestServiceGraph(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	created, err := p.User.Resource.CreateResource(t.Context(), &resourcev1.CreateResourceRequest{
		WorkspaceId: w.ID,
		Name:        "checkout",
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec: &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Service{
				Service: &resourcev1.ServiceSpec{
					Regions: map[string]*resourcev1.RegionTarget{
						p.Region: {Enabled: true, Cpu: "100m", Memory: "128Mi"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	query := fmt.Sprintf(
		`INSERT INTO otel_traces
 (Timestamp, TraceId, SpanId, SpanKind, ServiceName, Duration, StatusCode, ResourceAttributes, SpanAttributes)
 VALUES (fromUnixTimestamp64Nano(%d), 'graph-trace', 'graph-client', 'Client', 'sdk-name', 25000000, 'Error',
 map('loco.workspace.id','%s','loco.environment.id','%s','loco.resource.id','%s','loco.retention.days','1'),
 map('peer.service','payments'))`,
		at.UnixNano(),
		w.ID,
		w.EnvironmentID,
		created.GetResourceId(),
	)
	if _, err = p.ClickHouse(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	req := &observabilityv1.QueryServiceGraphRequest{
		WorkspaceId:   w.ID,
		EnvironmentId: w.EnvironmentID,
		StartTime:     timestamppb.New(at.Add(-time.Minute)),
		EndTime:       timestamppb.New(at.Add(time.Minute)),
	}
	graph, err := p.User.Observability.QueryServiceGraph(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.GetEdges()) != 1 {
		t.Fatalf("graph = %v", graph)
	}
	edge := graph.GetEdges()[0]
	if edge.GetFrom() != "checkout" || edge.GetTo() != "payments" || edge.GetRequests() != 1 || edge.GetErrors() != 1 ||
		edge.GetP95Ms() != 25 {
		t.Fatalf("edge = %v", edge)
	}
	second := w.NewWorkspace(t, "graph-isolation")
	req.WorkspaceId, req.EnvironmentId = second.ID, second.EnvironmentID
	empty, err := p.User.Observability.QueryServiceGraph(t.Context(), req)
	if err != nil || len(empty.GetEdges()) != 0 {
		t.Fatalf("isolated graph = %v (%v)", empty, err)
	}
}
