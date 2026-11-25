package telemetry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/systemtenant"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const systemReaderEmail = "system-telemetry-reader@test.local"

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

// TestSystemReaderReadsTheAPIsLogs checks that a user with the system read role reads the API's own
// log lines from the system tenant through the observability API, and that a user without it is denied.
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
			WorkspaceId: systemtenant.WorkspaceID,
			StartTime:   timestamppb.New(time.Now().Add(-time.Hour)),
			EndTime:     timestamppb.Now(),
			Query:       "@component:loco-api @cluster:" + systemtenant.ControlPlaneClusterID,
			PageSize:    1,
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
	_, err = query(p.User)
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("system scope without the role: code %v (%v), want permission_denied", code, err)
	}
}
