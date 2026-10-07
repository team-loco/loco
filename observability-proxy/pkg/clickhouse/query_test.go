package clickhouse

import (
	"slices"
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const (
	testWorkspaceID = "11111111-2222-3333-4444-555555555555"
	testResourceID  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func TestBuildLogsQueryFiltersOnStoredLabelKeys(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	query, args := buildLogsQuery(
		testWorkspaceID,
		[]string{testResourceID},
		start,
		end,
		"",
		nil,
		map[string]string{"app.loco.io/component": "web"},
		100,
		"",
		observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST,
		10,
	)

	wantClauses := []string{
		"ResourceAttributes['loco.io/resource-id'] AS resource_id",
		"WHERE ResourceAttributes['loco.io/workspace-id'] = ?",
		"ResourceAttributes['loco.io/resource-id'] IN (?)",
		"ResourceAttributes[?] = ?",
	}
	for _, clause := range wantClauses {
		if !strings.Contains(query, clause) {
			t.Errorf("query missing %q:\n%s", clause, query)
		}
	}
	if strings.Contains(query, "k8s.pod.labels.") {
		t.Errorf("query uses prefixed label keys:\n%s", query)
	}

	if len(args) < 2 {
		t.Fatalf("got %d args, want at least 2", len(args))
	}
	if args[0] != testWorkspaceID {
		t.Errorf("args[0] = %v, want workspace id %q", args[0], testWorkspaceID)
	}
	if args[1] != testResourceID {
		t.Errorf("args[1] = %v, want resource id %q", args[1], testResourceID)
	}
	if !slices.Contains(args, any("app.loco.io/component")) {
		t.Errorf("args missing raw label key: %v", args)
	}
}

func TestBuildMetricsQueryFiltersOnStoredLabelKeys(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	query, args := buildMetricsQuery(
		testWorkspaceID,
		[]string{testResourceID},
		start,
		end,
		"k8s.pod.cpu.usage",
		60,
		"avg",
		10,
	)

	wantClauses := []string{
		"ResourceAttributes['loco.io/resource-id'] AS resource_id",
		"FROM otel_metrics_gauge",
		"WHERE ResourceAttributes['loco.io/workspace-id'] = ?",
		"ResourceAttributes['loco.io/resource-id'] IN (?)",
	}
	for _, clause := range wantClauses {
		if !strings.Contains(query, clause) {
			t.Errorf("query missing %q:\n%s", clause, query)
		}
	}
	if strings.Contains(query, "k8s.pod.labels.") {
		t.Errorf("query uses prefixed label keys:\n%s", query)
	}

	if len(args) < 2 {
		t.Fatalf("got %d args, want at least 2", len(args))
	}
	if args[0] != testWorkspaceID {
		t.Errorf("args[0] = %v, want workspace id %q", args[0], testWorkspaceID)
	}
	if args[1] != testResourceID {
		t.Errorf("args[1] = %v, want resource id %q", args[1], testResourceID)
	}
}
