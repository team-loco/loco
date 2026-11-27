package clickhouse

import (
	"maps"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/capacitymetrics"
	"github.com/team-loco/loco/internal/systemtenant"
)

const capacityTestInterval = "60s"
const capacityFleetRuntime = "fleet-runtime"
const capacityFleetEmpty = "fleet-empty"
const capacityFleetPartial = "fleet-partial"
const capacityFleetBuild = "fleet-build"

const capacityTestNodeA = "node-a"
const capacityTestNodeB = "node-b"
const nativeNodeAttribute = "k8s.node.name"
const capacityClusterAttribute = "loco.cluster.id"

func TestCapacityUsesCompleteCollectionsAndPreservesScope(t *testing.T) {
	t.Parallel()
	f := newQueryFixture(t)
	const cluster = "cluster-a"
	const source = "source-a"
	at := time.Now().UTC().Truncate(time.Millisecond).Add(-2 * time.Minute)
	insert := func(metric string, when time.Time, value float64, labels map[string]string) {
		t.Helper()
		attrs := map[string]string{
			workspaceAttributeKey:    systemtenant.WorkspaceID,
			retentionAttributeKey:    "1",
			capacityClusterAttribute: cluster,
			nativeNodeAttribute:      "exporter-host",
			capacitymetrics.Interval: capacityTestInterval,
		}
		if err := f.conn.Exec(
			t.Context(),
			`INSERT INTO otel_metrics_gauge (ResourceAttributes,MetricName,Attributes,TimeUnix,Value)
VALUES (?,?,?,fromUnixTimestamp64Milli(?),?)`,
			attrs,
			metric,
			labels,
			when.UnixMilli(),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	manifest := func(when time.Time, count float64) {
		insert(capacitymetrics.Pods, when, count, map[string]string{capacitymetrics.Source: source})
	}
	point := func(when time.Time, pod, node, env, build string, cpu, memory float64) {
		labels := map[string]string{
			capacitymetrics.Source:       source,
			capacitymetrics.PodUID:       pod,
			capacitymetrics.Node:         node,
			capacitymetrics.Workspace:    testWorkspaceID,
			capacitymetrics.Organization: "organization-a",
			capacitymetrics.Environment:  env,
			capacitymetrics.Resource:     testResourceID,
			capacitymetrics.Build:        build,
		}
		insert(capacitymetrics.CPU, when, cpu, labels)
		insert(capacitymetrics.Memory, when, memory, labels)
	}
	if sample := capacitySampleForTest(t, f, cluster); sample != nil {
		t.Fatalf("empty coverage=%+v", sample)
	}
	point(at, "runtime", capacityTestNodeA, testEnvironmentID, "", 0.8, 256<<20)
	point(at, "build", capacityTestNodeB, "", "build-a", 2, 512<<20)
	manifest(at, 2)
	// Duplicate delivery does not change sample membership or totals.
	point(at, "runtime", capacityTestNodeA, testEnvironmentID, "", 0.8, 256<<20)
	newer := at.Add(time.Minute)
	manifest(newer, 2)
	point(newer, "runtime", capacityTestNodeA, testEnvironmentID, "", 0.2, 128<<20)
	sample := capacitySampleForTest(t, f, cluster)
	if !sample.At.Equal(at) {
		t.Fatalf("partial latest collection selected: %v", sample.At)
	}
	totals, err := f.client.CapacityTotals(
		t.Context(),
		*sample,
		CapacityScope{Group: CapacityByResource, Workspace: testWorkspaceID, Environment: testOtherEnvironmentID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) != 1 || totals[0].CPU != 0 || totals[0].Memory != 0 || totals[0].BuildCPU != 2000 ||
		totals[0].BuildMemory != 512<<20 {
		t.Fatalf("environment/build scope=%+v", totals)
	}
	totals, err = f.client.CapacityTotals(t.Context(), *sample, CapacityScope{Group: CapacityByCluster})
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) != 1 || totals[0].CPU != 800 || totals[0].BuildCPU != 2000 || totals[0].Resources != 1 {
		t.Fatalf("duplicate delivery/resource identity=%+v", totals)
	}
	nodes, err := f.client.CapacityTotals(
		t.Context(),
		*sample,
		CapacityScope{Group: CapacityByNode, Node: capacityTestNodeB},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Node != capacityTestNodeB || nodes[0].BuildCPU != 2000 {
		t.Fatalf("observed node was replaced by exporter host: %+v", nodes)
	}
	pinnedID := sample.ID()
	// Completing a newer cohort removes the departed build, rather than retaining
	// each pod's last gauge forever. It also handles an entirely empty cluster.
	point(newer, "build", capacityTestNodeB, "", "build-a", 1, 256<<20)
	sample = capacitySampleForTest(t, f, cluster)
	if !sample.At.Equal(newer) {
		t.Fatalf("completed latest=%+v %v", sample, err)
	}
	pinned, pinErr := f.client.CapacityClusterAt(t.Context(), cluster, pinnedID)
	if pinErr != nil {
		t.Fatal(pinErr)
	}
	if pinned.CPU != 2800 || pinned.Sample.ID() != pinnedID {
		t.Fatalf("new collection replaced pinned reservations: %+v", pinned)
	}
	empty := newer.Add(time.Second)
	manifest(empty, 0)
	sample = capacitySampleForTest(t, f, cluster)
	totals, err = f.client.CapacityTotals(t.Context(), *sample, CapacityScope{Group: CapacityByCluster})
	if err != nil {
		t.Fatal(err)
	}
	if !sample.At.Equal(empty) || len(totals) != 0 {
		t.Fatalf("empty complete collection retains old pods: %+v %+v", sample, totals)
	}
	if sample = capacitySampleForTest(t, f, "other-cluster"); sample != nil {
		t.Fatalf("cross-cluster coverage=%+v", sample)
	}
}

func TestNodeCapacityRetainsMissingDimensionsAndOwnFreshness(t *testing.T) {
	t.Parallel()
	f := newQueryFixture(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	for _, row := range []struct {
		node, metric string
		at           time.Time
		value        float64
	}{
		{capacityTestNodeA, "k8s.node.allocatable_cpu", at, 4},
		{capacityTestNodeA, "k8s.node.allocatable_memory", at, 8 << 30},
		{capacityTestNodeA, "k8s.node.condition_ready", at, 1},
		{capacityTestNodeB, "k8s.node.allocatable_cpu", at.Add(-10 * time.Minute), 2},
		{"unknown-node", "k8s.node.condition_ready", at, -1},
	} {
		attrs := map[string]string{
			workspaceAttributeKey:    systemtenant.WorkspaceID,
			retentionAttributeKey:    "1",
			capacityClusterAttribute: "native-cluster",
			nativeNodeAttribute:      row.node,
			capacitymetrics.Interval: capacityTestInterval,
		}
		if err := f.conn.Exec(
			t.Context(),
			`INSERT INTO otel_metrics_gauge (ResourceAttributes,MetricName,TimeUnix,Value)
VALUES (?,?,fromUnixTimestamp64Milli(?),?)`,
			attrs,
			row.metric,
			row.at.UnixMilli(),
			row.value,
		); err != nil {
			t.Fatal(err)
		}
	}
	data, err := f.client.CapacityClusters(t.Context(), []string{"native-cluster"})
	rows := data[0].Nodes
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("nodes=%+v", rows)
	}
	for _, row := range rows {
		switch row.Name {
		case capacityTestNodeA:
			if row.CPU == nil || *row.CPU != 4000 || row.Memory == nil || *row.Memory != 8<<30 || row.Ready == nil ||
				!*row.Ready ||
				row.Stale {
				t.Fatalf("fresh node=%+v", row)
			}
		case "unknown-node":
			if row.Ready != nil {
				t.Fatalf("receiver unknown condition was treated as ready: %+v", row)
			}
		case capacityTestNodeB:
			if row.CPU == nil || row.Memory != nil || row.Ready != nil || !row.Stale {
				t.Fatalf("missing dimensions represented as zero=%+v", row)
			}
		}
	}
}

func TestCapacityFleetBatchesIndependentClustersAndInventories(t *testing.T) {
	t.Parallel()
	f := newQueryFixture(t)
	at := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Minute)
	const source = "fleet-source"
	insert := func(cluster, metric string, value float64, labels map[string]string) {
		t.Helper()
		attrs := map[string]string{
			workspaceAttributeKey:    systemtenant.WorkspaceID,
			retentionAttributeKey:    "1",
			capacityClusterAttribute: cluster,
			nativeNodeAttribute:      capacityTestNodeA,
			capacitymetrics.Interval: capacityTestInterval,
		}
		if err := f.conn.Exec(t.Context(), `INSERT INTO otel_metrics_gauge
(ResourceAttributes,MetricName,Attributes,TimeUnix,Value) VALUES (?,?,?,fromUnixTimestamp64Milli(?),?)`,
			attrs, metric, labels, at.UnixMilli(), value); err != nil {
			t.Fatal(err)
		}
	}
	labels := map[string]string{
		capacitymetrics.Source:      source,
		capacitymetrics.PodUID:      "same-uid",
		capacitymetrics.Node:        capacityTestNodeA,
		capacitymetrics.Workspace:   testWorkspaceID,
		capacitymetrics.Resource:    testResourceID,
		capacitymetrics.Environment: testEnvironmentID,
	}
	for _, cluster := range []string{capacityFleetRuntime, capacityFleetBuild, capacityFleetPartial, capacityFleetEmpty} {
		insert(cluster, "k8s.node.allocatable_cpu", 4, nil)
		expected := 1.0
		if cluster == capacityFleetEmpty {
			expected = 0
		}
		insert(cluster, capacitymetrics.Pods, expected, map[string]string{capacitymetrics.Source: source})
		if cluster == capacityFleetEmpty {
			continue
		}
		points := map[string]string{}
		maps.Copy(points, labels)
		if cluster == capacityFleetBuild {
			points[capacitymetrics.Environment] = ""
			points[capacitymetrics.Build] = "standalone-build"
		}
		insert(cluster, capacitymetrics.CPU, 0.5, points)
		if cluster != capacityFleetPartial {
			insert(cluster, capacitymetrics.Memory, 128<<20, points)
		}
	}
	fleet, err := f.client.CapacityClusters(
		t.Context(),
		[]string{capacityFleetRuntime, capacityFleetBuild, capacityFleetPartial, capacityFleetEmpty, "fleet-missing"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(fleet) != 5 {
		t.Fatalf("fleet=%+v", fleet)
	}
	for _, cluster := range fleet {
		switch cluster.Cluster {
		case capacityFleetRuntime, capacityFleetBuild:
			if cluster.Sample == nil || cluster.CPU != 500 || cluster.Resources != 1 || len(cluster.Nodes) != 1 ||
				cluster.Nodes[0].RequestedCPU != 500 {
				t.Fatalf("cluster reservations=%+v", cluster)
			}
		case capacityFleetPartial, "fleet-missing":
			if cluster.Sample != nil {
				t.Fatalf("unknown cluster presented as complete: %+v", cluster)
			}
		case capacityFleetEmpty:
			if cluster.Sample == nil || cluster.CPU != 0 {
				t.Fatalf("known empty cluster=%+v", cluster)
			}
		}
	}
	totals, samples, err := f.client.CapacityInventory(
		t.Context(),
		[]string{capacityFleetRuntime, capacityFleetBuild, capacityFleetPartial, capacityFleetEmpty},
		CapacityScope{
			Group:       CapacityByResource,
			Workspace:   testWorkspaceID,
			Environment: testEnvironmentID,
			Resources:   []string{testResourceID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 3 || len(totals) != 1 || totals[0].CPU != 500 || totals[0].BuildCPU != 500 ||
		totals[0].Resources != 1 {
		t.Fatalf("inventory duplicates cross-cluster UID or mixes builds: %+v %+v", samples, totals)
	}
	totals, _, err = f.client.CapacityInventory(
		t.Context(),
		[]string{capacityFleetRuntime},
		CapacityScope{Group: CapacityByWorkspace, Workspaces: []string{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) != 0 {
		t.Fatalf("empty authorized workspace set leaked totals: %+v", totals)
	}
}

func capacitySampleForTest(t *testing.T, f *queryFixture, cluster string) *CapacitySample {
	t.Helper()
	rows, err := f.client.CapacityClusters(t.Context(), []string{cluster})
	if err != nil {
		t.Fatal(err)
	}
	return rows[0].Sample
}
