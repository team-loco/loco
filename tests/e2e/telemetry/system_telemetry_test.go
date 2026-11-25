package telemetry

import (
	"fmt"
	"testing"

	"github.com/team-loco/loco/internal/systemtenant"
)

// TestAPITelemetryReachesTheSystemTenant checks that the API's own logs, traces and metrics,
// exported with the system ingest token, land under the system tenant stamped as the control plane.
func TestAPITelemetryReachesTheSystemTenant(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"otel_logs", "otel_traces", "otel_metrics_sum"} {
		waitCount(t, "the API's own rows in "+table, fmt.Sprintf(
			"SELECT count() FROM %s WHERE WorkspaceId = '%s' AND ServiceName = 'loco-api' "+
				"AND ResourceAttributes['loco.cluster.id'] = '%s' AND OrganizationId = ''",
			table, systemtenant.WorkspaceID, systemtenant.ControlPlaneClusterID,
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
