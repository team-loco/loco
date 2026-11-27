package telemetry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/systemtenant"
	"github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const systemReaderEmail = "system-reader@test.local"

// TestAPITelemetryReachesTheSystemTenant checks that the API's own logs, traces and metrics,
// exported with the system ingest token, land under the system tenant stamped as the control plane.
func TestAPITelemetryReachesTheSystemTenant(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"otel_logs", "otel_traces", "otel_metrics_sum"} {
		waitCount(t, "the API's own rows in "+table, fmt.Sprintf(
			"SELECT count() FROM %s WHERE WorkspaceId = '%s' AND ServiceName = 'loco-api' "+
				"AND ResourceAttributes['loco.cluster.id'] = '%s' AND OrganizationId = '' "+
				"AND ResourceAttributes['loco.region'] = '%s'",
			table, systemtenant.WorkspaceID, systemtenant.ControlPlaneClusterID, p.Region,
		), func(count int) bool { return count > 0 })
	}
}

// TestControllerTelemetryReachesTheSystemTenant checks that the controller's metrics, sent through the
// cluster collector's platform pipeline with the cluster's ingest token, land under the system tenant
// stamped with that cluster.
func TestControllerTelemetryReachesTheSystemTenant(t *testing.T) {
	t.Parallel()
	waitCount(t, "the controller's reconcile metrics", fmt.Sprintf(
		"SELECT count() FROM otel_metrics_sum WHERE WorkspaceId = '%s' AND ServiceName = 'loco-controller' "+
			"AND MetricName = 'controller_runtime_reconcile_total' AND ResourceAttributes['loco.cluster.id'] = '%s' "+
			"AND ResourceAttributes['loco.telemetry.scope'] = 'platform'",
		systemtenant.WorkspaceID, p.ClusterID,
	), func(count int) bool { return count > 0 })
}

// TestSystemReaderReadsTheAPIsLogs checks that a user with the system read role reads the API's own
// log lines from the system tenant through the observability API's installation scope, that the system
// tenant's ID is not a workspace, and that a user without the role is denied.
func TestSystemReaderReadsTheAPIsLogs(t *testing.T) {
	t.Parallel()
	web := p.Clients(p.ProviderToken(t, systemReaderEmail))
	code, err := platform.ApproveCLILogin(t.Context(), web)
	if err != nil {
		t.Fatal(err)
	}
	exchanged, err := p.Clients("").Auth.ExchangeCLICode(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	reader := p.Clients(exchanged.GetTokens().GetAccessToken())
	me, err := reader.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Admin.SystemRole.GrantSystemRole(t.Context(), &adminv1.GrantSystemRoleRequest{
		UserId: me.GetUser().GetId(), Role: adminv1.SystemRole_SYSTEM_ROLE_READ,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, revokeErr := p.Admin.SystemRole.RevokeSystemRole(context.Background(), &adminv1.RevokeSystemRoleRequest{
			UserId: me.GetUser().GetId(),
		}); revokeErr != nil {
			t.Errorf("revoke system read: %v", revokeErr)
		}
	})

	query := func(clients *rpc.Clients) (*observabilityv1.QueryLogsResponse, error) {
		return clients.Observability.QueryLogs(t.Context(), &observabilityv1.QueryLogsRequest{
			Installation: true,
			StartTime:    timestamppb.New(time.Now().Add(-time.Hour)),
			EndTime:      timestamppb.Now(),
			Query:        "@component:loco-api @cluster:" + systemtenant.ControlPlaneClusterID,
			PageSize:     1,
		})
	}
	platform.Eventually(t, telemetryTimeout, "an API log line read as a system reader", func(context.Context) error {
		response, queryErr := query(reader)
		if queryErr != nil {
			return queryErr
		}
		if len(response.GetEntries()) == 0 {
			return errors.New("no API log lines yet")
		}
		return nil
	})
	_, err = reader.Observability.QueryLogs(t.Context(), &observabilityv1.QueryLogsRequest{
		WorkspaceId: systemtenant.WorkspaceID,
		StartTime:   timestamppb.New(time.Now().Add(-time.Hour)),
		EndTime:     timestamppb.Now(),
	})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("system tenant ID as a workspace: code %v (%v), want permission_denied", code, err)
	}
	_, err = query(p.User)
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("system scope without the role: code %v (%v), want permission_denied", code, err)
	}
}

// TestProxyTelemetryReachesTheSystemTenant exercises the proxy's real HTTP ingestion and
// configured exporter, then reads its span back from ClickHouse under the system tenant.
func TestProxyTelemetryReachesTheSystemTenant(t *testing.T) {
	t.Parallel()
	var token string
	platform.Eventually(t, ingestTokenTimeout, "the cluster ingest token", func(ctx context.Context) error {
		var err error
		token, err = p.IngestToken(ctx, p.CollectorNamespace())
		if err == nil && token == "" {
			return errors.New("empty ingest token")
		}
		return err
	})
	status, rejected := postLogs(t, t.Context(), "", "proxy platform telemetry e2e", "Bearer "+token)
	if status != http.StatusOK || rejected != 0 {
		t.Fatalf("cluster log ingestion: status %d, rejected %d", status, rejected)
	}
	waitCount(t, "the proxy's own ingestion span", fmt.Sprintf(
		"SELECT count() FROM otel_traces WHERE WorkspaceId = '%s' AND ServiceName = 'loco-obs-proxy' "+
			"AND SpanName = 'ingest.logs' AND ResourceAttributes['loco.cluster.id'] = '%s' AND OrganizationId = ''",
		systemtenant.WorkspaceID, systemtenant.ControlPlaneClusterID), func(count int) bool { return count > 0 })
}

func TestSystemMetricsSelectNodeResourceIdentity(t *testing.T) {
	t.Parallel()
	var token string
	platform.Eventually(t, ingestTokenTimeout, "the cluster ingest token", func(ctx context.Context) error {
		var err error
		token, err = p.IngestToken(ctx, p.CollectorNamespace())
		if err == nil && token == "" {
			return errors.New("empty ingest token")
		}
		return err
	})
	const name = "e2e.node.identity"
	timestamp := time.Now().Truncate(time.Second)
	metrics := pmetric.NewMetrics()
	samples := []struct {
		node  string
		value float64
	}{{"e2e-node-a", 0.2}, {"e2e-node-b", 0.8}}
	for _, sample := range samples {
		resource := metrics.ResourceMetrics().AppendEmpty()
		resource.Resource().Attributes().PutStr("loco.telemetry.scope", "platform")
		resource.Resource().Attributes().PutStr("k8s.node.name", sample.node)
		metric := resource.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		metric.SetName(name)
		point := metric.SetEmptyGauge().DataPoints().AppendEmpty()
		point.SetTimestamp(pcommon.NewTimestampFromTime(timestamp))
		point.SetDoubleValue(sample.value)
		// A point label must not override the node identity from its resource.
		point.Attributes().PutStr("node", "e2e-node-decoy")
	}
	payload, err := pmetricotlp.NewExportRequestFromMetrics(metrics).MarshalProto()
	if err != nil {
		t.Fatal(err)
	}
	response := do(t, t.Context(), http.MethodPost, p.OTLPURL+"/v1/metrics", map[string]string{
		contentType: protobufContentType, authorizationHeader: "Bearer " + token,
	}, payload)
	if response.status != http.StatusOK {
		t.Fatalf("metric ingestion status %d: %s", response.status, response.body)
	}
	decoded := pmetricotlp.NewExportResponse()
	if err = decoded.UnmarshalProto(response.body); err != nil {
		t.Fatal(err)
	}
	if rejected := decoded.PartialSuccess().RejectedDataPoints(); rejected != 0 {
		t.Fatalf("rejected %d metric points", rejected)
	}
	query := func(node string) (*observabilityv1.QueryMetricsResponse, error) {
		return p.Admin.Observability.QueryMetrics(t.Context(), &observabilityv1.QueryMetricsRequest{
			Installation:    true,
			MetricName:      name,
			Aggregation:     "avg",
			IntervalSeconds: 60,
			StartTime:       timestamppb.New(timestamp.Add(-time.Minute)),
			EndTime:         timestamppb.New(timestamp.Add(time.Minute)),
			Attributes:      map[string]string{"cluster": p.ClusterID, "node": node},
		})
	}
	platform.Eventually(
		t,
		telemetryTimeout,
		"node-specific metrics through OTLP and ClickHouse",
		func(context.Context) error {
			for _, sample := range samples {
				result, queryErr := query(sample.node)
				if queryErr != nil {
					return queryErr
				}
				if len(result.GetSeries()) != 1 || len(result.GetSeries()[0].GetPoints()) != 1 {
					return fmt.Errorf("node %s: want one point, got %v", sample.node, result)
				}
				if value := result.GetSeries()[0].GetPoints()[0].GetValue(); math.Abs(value-sample.value) > 1e-9 {
					return fmt.Errorf("node %s: value %g, want %g", sample.node, value, sample.value)
				}
			}
			return nil
		},
	)
	result, err := query("e2e-node-decoy")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.GetSeries()) != 0 {
		t.Fatalf("point label selected unrelated node data: %v", result)
	}
}

// TestControllerLogsLinkToTheirReconcile follows a real deployment's diagnostic through
// the controller, cluster collector, ingest and the installation-scoped read API.
func TestControllerLogsLinkToTheirReconcile(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const name = "controller-logs"
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, locofile.File{
		Partial:  name,
		Services: map[string]locofile.Service{name: platform.ImageService(p.AlternatePortImage, w.Host(name))},
	})
	start := time.Now()
	w.MustLoco(t, dir, "deploy", "--yes")
	resourceID := w.ResourceID(t, name)
	platform.Eventually(
		t,
		telemetryTimeout,
		"a controller diagnostic linked to this deployment's reconcile",
		func(ctx context.Context) error {
			logs, err := p.Admin.Observability.QueryLogs(ctx, &observabilityv1.QueryLogsRequest{
				Installation: true, StartTime: timestamppb.New(start), EndTime: timestamppb.Now(),
				Query: "@component:loco-controller @cluster:" + p.ClusterID, PageSize: 1000,
			})
			if err != nil {
				return err
			}
			for _, entry := range logs.GetEntries() {
				if entry.GetLogAttributes()["resource"] != resourceID {
					continue
				}
				if entry.GetLogAttributes()["reason"] != string(v1alpha1.ProblemHealthCheckFailing) {
					continue
				}
				if entry.GetLogAttributes()["detail"] != "Health check GET :8080/ failed: connection refused" {
					continue
				}
				if entry.GetResourceAttributes()["loco.cluster.id"] != p.ClusterID {
					return errors.New("diagnostic lost its cluster identity")
				}
				if entry.GetTraceId() == "" || entry.GetSpanId() == "" {
					return errors.New("controller diagnostic has no trace/span IDs")
				}
				trace, err := p.Admin.Observability.GetTrace(ctx, &observabilityv1.GetTraceRequest{
					Scope: &observabilityv1.TraceScope{Installation: true}, TraceId: entry.GetTraceId(),
				})
				if err != nil {
					return err
				}
				for _, span := range trace.GetSpans() {
					if span.GetSpanId() == entry.GetSpanId() && span.GetName() == "application.status" {
						t.Logf(
							"controller diagnostic read through API: body=%q reason=%s detail=%q trace=%s span=%s resource=%s cluster=%s",
							entry.GetBody(),
							entry.GetLogAttributes()["reason"],
							entry.GetLogAttributes()["detail"],
							entry.GetTraceId(),
							entry.GetSpanId(),
							resourceID,
							entry.GetResourceAttributes()["loco.cluster.id"],
						)
						return nil
					}
				}
			}
			return errors.New("no correlated controller diagnostic for this deployment yet")
		},
	)
}
