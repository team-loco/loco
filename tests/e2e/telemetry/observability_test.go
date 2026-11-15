package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	otelApp          = "e2e-otel-app"
	requestCount     = 5
	routeTimeout     = 3 * time.Minute
	telemetryTimeout = 4 * time.Minute
	eventFlushWindow = 10 * time.Second
	ingestReplicas   = 2
	proxyLimit       = 50
	metricInterval   = 60
	crashRecords     = 50
	crashFlush       = 30 * time.Second
	// The otel-app fixture's tracerName, span link and forged attributes, in tests/e2e/fixtures/otel-app/main.go.
	fixtureScope         = "github.com/team-loco/loco/tests/e2e/fixtures/otel-app"
	fixtureLinkTrace     = "0102030405060708090a0b0c0d0e0f10"
	fixtureLinkSpan      = "0102030405060708"
	fixtureLinkAttribute = "retry"
	forgedSpan           = "forged-workspace-span"
	forgedWorkspace      = "forged-workspace"
	forgedOrganization   = "forged-organization"
	organizationAttr     = "loco.organization.id"
	sampledTraceFlags    = "01"
	otlpProtobuf         = "http/protobuf"
	metricTablesSelected = "otel_metrics_sum otel_metrics_gauge otel_metrics_histogram"
	allTables            = "otel_traces otel_logs otel_metrics_sum otel_metrics_gauge otel_metrics_histogram " +
		"otel_metrics_exponential_histogram otel_metrics_summary"
)

// TestObservability follows telemetry from an application deployed through Loco, its gateway and
// its cluster, over the collectors and OTLP ingest into ClickHouse, and back out through the API:
// injected SDK settings, cluster events stored once, traces, logs, access logs, Envoy and cluster
// metrics, forged tenancy attributes, workspace isolation, user-owned SDK settings, injection
// turned off, dependency spans, and replay of acknowledged logs after a collector SIGKILL.
func TestObservability(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	second := w.NewWorkspace(t, w.Name+"-isolation")
	dir := t.TempDir()
	p.CopyFixture(t, "otel-app", dir)
	host := platform.Host(otelApp)
	file := locofile.File{
		Partial:  otelApp,
		Services: map[string]locofile.Service{otelApp: platform.SourceService(host)},
	}
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	resourceID := w.ResourceID(t, otelApp)
	application, err := p.Application(t.Context(), resourceID)
	if err != nil {
		t.Fatal(err)
	}
	tenant := application.Spec
	if tenant.WorkspaceID != w.ID || tenant.EnvironmentID == "" || tenant.OrganizationID != w.OrgID {
		t.Fatalf("the Application carries workspace %q, environment %q, organization %q; want %s, set, %s",
			tenant.WorkspaceID, tenant.EnvironmentID, tenant.OrganizationID, w.ID, w.OrgID)
	}
	own := p.Clients(w.MintToken(t, "e2e-obs-own", tokenv1.Scope_SCOPE_READ))
	other := p.Clients(second.MintToken(t, "e2e-obs-second", tokenv1.Scope_SCOPE_READ))
	t.Logf("fixture %s in workspace %s, environment %s, organization %s; second workspace %s",
		resourceID, w.ID, tenant.EnvironmentID, w.OrgID, second.ID)

	o := &observed{w: w, second: second, dir: dir, file: file, host: host, resourceID: resourceID, tenant: tenant,
		own: own, other: other, traceID: randomHex(16), clientSpan: randomHex(8), marker: "obs-" + randomHex(6)}
	t.Run("application telemetry is configured by default", o.defaultTelemetry)

	t.Run("cluster events are collected once", clusterEventsOnce)

	t.Run("traffic through the gateway", o.traffic)

	t.Run("migrations", migrations)

	t.Run("ListTraces summarizes the known trace", o.traceSummary)

	t.Run("GetTrace returns the ingress trace", o.ingressTrace)

	t.Run("application logs carry the tenancy", o.appLogs)

	t.Run("gateway access logs carry the tenancy", o.accessLogs)

	t.Run("Envoy metrics carry the tenancy", o.envoyMetrics)

	t.Run("forged tenancy attributes are replaced", o.forgedAttributes)

	t.Run("QueryLogs is scoped to the workspace and its regions", o.scopedLogs)

	t.Run("QueryLogs filters access logs by log attributes", o.attributeLogs)

	t.Run("QueryMetrics returns the fixture's counters to its workspace only", o.counterMetrics)

	t.Run("cluster metrics reach the API", o.clusterMetrics)

	t.Run("reads are isolated between workspaces", o.isolation)

	t.Run("a user SDK configuration suppresses the injected bundle", o.userSDKConfiguration)

	t.Run("disabling injection keeps collection", o.injectionDisabled)

	t.Run("dependency spans keep namespace ownership", o.dependencySpans)

	t.Run("acknowledged logs replay after a collector SIGKILL", collectorCrashReplay)
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buffer)
}

// gatewayStatus sends a GET for host's path through the gateway and returns its HTTP status.
func gatewayStatus(ctx context.Context, host, path, traceparent string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.GatewayURL+path, nil)
	if err != nil {
		return 0, err
	}
	request.Host = host
	if traceparent != "" {
		request.Header.Set("Traceparent", traceparent)
	}
	client := http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	return response.StatusCode, response.Body.Close()
}

func containerEnv(t *testing.T, w *platform.Workspace, resourceID string) map[string]string {
	t.Helper()
	env, err := containerEnvCtx(t.Context(), w, resourceID)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func containerEnvCtx(ctx context.Context, w *platform.Workspace, resourceID string) (map[string]string, error) {
	var deployment appsv1.Deployment
	if err := p.Get(ctx, &deployment, "-n", w.Namespace, "deployment", "resource-"+resourceID); err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, variable := range deployment.Spec.Template.Spec.Containers[0].Env {
		env[variable.Name] = variable.Value
	}
	return env, nil
}

func telemetryEnvMatches(ctx context.Context, w *platform.Workspace, resourceID, service string) error {
	env, err := containerEnvCtx(ctx, w, resourceID)
	if err != nil {
		return err
	}
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != p.CollectorEndpoint() ||
		env["OTEL_EXPORTER_OTLP_PROTOCOL"] != otlpProtobuf ||
		env["OTEL_SERVICE_NAME"] != service {
		return fmt.Errorf("SDK environment %v", env)
	}
	return nil
}

func waitRows(t *testing.T, what, query string, check func([][]string) error) {
	t.Helper()
	platform.Eventually(t, telemetryTimeout, what, func(ctx context.Context) error {
		rows, err := p.ClickHouse(ctx, query)
		if err != nil {
			return err
		}
		if err := check(rows); err != nil {
			return err
		}
		t.Logf("%s:\n%s\n%v", what, query, rows)
		return nil
	})
}

func waitCount(t *testing.T, what, query string, ok func(int) bool) {
	t.Helper()
	platform.Eventually(t, telemetryTimeout, what, func(ctx context.Context) error {
		count, err := p.ClickHouseCount(ctx, query)
		if err == nil && !ok(count) {
			return fmt.Errorf("count %d", count)
		}
		return err
	})
}

func anyRow(want ...string) func([][]string) error {
	return func(rows [][]string) error {
		for _, row := range rows {
			if strings.Join(row, "\t") == strings.Join(want, "\t") {
				return nil
			}
		}
		return fmt.Errorf("%d rows, none %v", len(rows), want)
	}
}

func metricsUnion(columns string, since time.Time) string {
	union := make([]string, 0, 3)
	for table := range strings.FieldsSeq(metricTablesSelected) {
		union = append(
			union,
			fmt.Sprintf("SELECT %s FROM %s WHERE TimeUnix >= toDateTime(%d)", columns, table, since.Unix()),
		)
	}
	return strings.Join(union, " UNION ALL ")
}

func latestMigration(t *testing.T) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(p.Root(), "internal", "clickhouse", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	latest := 0
	for _, file := range files {
		version, err := strconv.Atoi(strings.SplitN(filepath.Base(file), "_", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		latest = max(latest, version)
	}
	return latest
}

// ingressTraceLinked checks every span belongs to the workspace and trace, and an application
// span of the fixture has a parent span of the same tenancy that the application did not emit.
func ingressTraceLinked(spans []*observabilityv1.TraceSpan, workspaceID, resourceID, orgID, traceID string) error {
	application := func(span *observabilityv1.TraceSpan) bool {
		return span.GetScopeName() == fixtureScope && span.GetResourceAttributes()[organizationAttr] == orgID
	}
	tenant := func(span *observabilityv1.TraceSpan) bool {
		return span.GetWorkspaceId() == workspaceID && span.GetResourceId() == resourceID
	}
	for _, span := range spans {
		if span.GetWorkspaceId() != workspaceID || span.GetTraceId() != traceID {
			return fmt.Errorf(
				"span %s of workspace %s and trace %s",
				span.GetSpanId(),
				span.GetWorkspaceId(),
				span.GetTraceId(),
			)
		}
	}
	for _, child := range spans {
		if !application(child) || !tenant(child) {
			continue
		}
		for _, parent := range spans {
			if !application(parent) && tenant(parent) && parent.GetSpanId() == child.GetParentSpanId() {
				return nil
			}
		}
	}
	return fmt.Errorf("%d spans, no application span under a gateway span", len(spans))
}

// fixtureLinkReturned checks a span of the fixture carries the span link the fixture adds.
func fixtureLinkReturned(spans []*observabilityv1.TraceSpan) error {
	for _, span := range spans {
		if span.GetScopeName() != fixtureScope {
			continue
		}
		for _, link := range span.GetLinks() {
			if link.GetTraceId() == fixtureLinkTrace && link.GetSpanId() == fixtureLinkSpan &&
				link.GetAttributes()["test.link"] == fixtureLinkAttribute {
				return nil
			}
		}
	}
	return fmt.Errorf("no span of %s carries the fixture's link", fixtureScope)
}

// dependencySpans runs the fixture's image as a pod without resource labels and with forged
// resource attributes, and checks its spans keep the namespace's tenancy without a resource.
func (o *observed) dependencySpans(t *testing.T) {
	w, second, resourceID, environmentID, own, other := o.w, o.second, o.resourceID, o.tenant.EnvironmentID, o.own, o.other
	dependency := "obs-dependency-" + randomHex(6)
	var deployment appsv1.Deployment
	p.MustGet(t, &deployment, "-n", w.Namespace, "deployment", "resource-"+resourceID)
	// Reuse the built fixture and its pull credentials, without resource ownership labels.
	spec := deployment.Spec.Template.Spec
	spec.Containers[0].Name = "dependency"
	spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: p.CollectorEndpoint()},
		{Name: "OTEL_EXPORTER_OTLP_PROTOCOL", Value: otlpProtobuf},
		{Name: "OTEL_SERVICE_NAME", Value: dependency},
		{
			Name: "OTEL_RESOURCE_ATTRIBUTES",
			Value: "k8s.namespace.name=" + second.Namespace + ",k8s.pod.name=forged-pod" +
				",loco.workspace.id=" + second.ID + ",loco.resource.id=" + resourceID,
		},
	}
	pod := corev1.Pod{APIVersion: "v1", Kind: "Pod", Name: dependency, Namespace: w.Namespace, Spec: spec}
	manifest, err := json.Marshal(pod)
	if err != nil {
		t.Fatal(err)
	}
	p.MustApply(t, string(manifest))
	p.DeleteOnCleanup(t, "-n", w.Namespace, "pod", dependency)
	p.MustKubectl(
		t,
		"-n",
		w.Namespace,
		"wait",
		"--for=condition=Ready",
		"pod/"+dependency,
		"--timeout",
		routeTimeout.String(),
	)
	port := p.PortForward(t, w.Namespace, "pod/"+dependency, 8080)
	traceID := randomHex(16)
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/dependency", port),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Traceparent", "00-"+traceID+"-"+randomHex(8)+"-"+sampledTraceFlags)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Error(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the dependency answered HTTP %d", response.StatusCode)
	}
	scope := &observabilityv1.TraceScope{WorkspaceId: w.ID}
	platform.Eventually(
		t,
		telemetryTimeout,
		"the dependency trace with namespace ownership",
		func(ctx context.Context) error {
			trace, getErr := own.Observability.GetTrace(
				ctx,
				&observabilityv1.GetTraceRequest{Scope: scope, TraceId: traceID},
			)
			if getErr != nil {
				return getErr
			}
			if len(trace.GetSpans()) != 1 {
				return fmt.Errorf("%d spans", len(trace.GetSpans()))
			}
			span := trace.GetSpans()[0]
			attributes := span.GetResourceAttributes()
			if span.GetServiceName() != dependency || span.GetWorkspaceId() != w.ID ||
				span.GetEnvironmentId() != environmentID ||
				span.GetResourceId() != "" ||
				attributes[organizationAttr] != w.OrgID ||
				attributes["loco.resource.id"] != "" ||
				attributes["k8s.namespace.name"] != w.Namespace {
				return fmt.Errorf("span %v", span)
			}
			return nil
		},
	)
	_, err = other.Observability.GetTrace(t.Context(), &observabilityv1.GetTraceRequest{Scope: scope, TraceId: traceID})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Errorf("the second workspace reading the dependency trace: code %v, want permission_denied", code)
	}
	trace, err := other.Observability.GetTrace(
		t.Context(),
		&observabilityv1.GetTraceRequest{Scope: &observabilityv1.TraceScope{WorkspaceId: second.ID}, TraceId: traceID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.GetSpans()) != 0 {
		t.Errorf("the forged workspace holds %d dependency spans", len(trace.GetSpans()))
	}
}

// collectorCrashReplay holds acknowledged logs in the OTLP gateway collector's queue, SIGKILLs it
// before the queue flushes, and checks every log arrives after the restart.
func collectorCrashReplay(t *testing.T) {
	namespace, collector := p.CollectorNamespace(), p.CollectorName()
	var config corev1.ConfigMap
	p.MustGet(t, &config, "-n", namespace, "configmap", collector)
	original := config.DeepCopy()
	t.Cleanup(func() { restoreCollector(t, original) })
	var relay map[string]any
	if err := yaml.Unmarshal([]byte(config.Data["relay"]), &relay); err != nil {
		t.Fatal(err)
	}
	exporter, ok := relay["exporters"].(map[string]any)["otlp_http"].(map[string]any)
	if !ok {
		t.Fatal("the relay configuration has no otlp_http exporter")
	}
	queue, ok := exporter["sending_queue"].(map[string]any)
	if !ok {
		queue = map[string]any{}
		exporter["sending_queue"] = queue
	}
	// Keep the acknowledged records below the flush threshold until SIGKILL reaches the process.
	queue["batch"] = map[string]any{"sizer": "items", "min_size": 100000, "flush_timeout": crashFlush.String()}
	data, err := yaml.Marshal(relay)
	if err != nil {
		t.Fatal(err)
	}
	config.Data["relay"] = string(data)
	manifest, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Replace(t.Context(), string(manifest)); err != nil {
		t.Fatal(err)
	}
	windowStarted := time.Now()
	p.MustKubectl(t, "-n", namespace, "rollout", "restart", "statefulset/"+collector)
	p.MustKubectl(t, "-n", namespace, "rollout", "status", "statefulset/"+collector, "--timeout", routeTimeout.String())
	podName := collector + "-0"
	var pod corev1.Pod
	p.MustGet(t, &pod, "-n", namespace, "pod", podName)
	container := pod.Status.ContainerStatuses[0].ContainerID
	inspect, err := p.Command(
		t.Context(),
		"docker",
		"exec",
		pod.Spec.NodeName,
		"crictl",
		"inspect",
		strings.TrimPrefix(container, "containerd://"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Info struct {
			PID int `json:"pid"`
		} `json:"info"`
	}
	if err = json.Unmarshal([]byte(inspect), &info); err != nil || info.Info.PID <= 0 {
		t.Fatalf("read the collector's PID: %v", err)
	}
	otlpPort := 0
	for _, port := range pod.Spec.Containers[0].Ports {
		if port.Name == "otlp-http" {
			otlpPort = int(port.ContainerPort)
		}
	}
	if _, err = p.Command(t.Context(), "docker", "stop", p.IngestContainer()); err != nil {
		t.Fatal(err)
	}
	local := p.PortForward(t, namespace, "pod/"+podName, otlpPort)
	marker := "collector-crash-" + randomHex(6)
	now := strconv.FormatInt(time.Now().Unix(), 10) + "000000000"
	records := make([]string, crashRecords)
	for i := range records {
		records[i] = fmt.Sprintf(`{"timeUnixNano":%q,"body":{"stringValue":"%s-%d"}}`, now, marker, i+1)
	}
	body := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + strings.Join(records, ",") + `]}]}]}`
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/logs", local),
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		PartialSuccess struct {
			RejectedLogRecords json.Number `json:"rejectedLogRecords"`
		} `json:"partialSuccess"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&exported)
	if err := response.Body.Close(); err != nil {
		t.Error(err)
	}
	if response.StatusCode != http.StatusOK || decodeErr != nil ||
		exported.PartialSuccess.RejectedLogRecords.String() != "" &&
			exported.PartialSuccess.RejectedLogRecords.String() != "0" {
		t.Fatalf(
			"the collector answered HTTP %d (%v) with %s rejected",
			response.StatusCode,
			decodeErr,
			exported.PartialSuccess.RejectedLogRecords,
		)
	}
	if _, err := p.Command(
		t.Context(),
		"docker",
		"exec",
		pod.Spec.NodeName,
		"kill",
		"-9",
		strconv.Itoa(info.Info.PID),
	); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(windowStarted); elapsed >= crashFlush {
		t.Fatalf("the SIGKILL came %s after the restart, past the %s flush", elapsed, crashFlush)
	}
	platform.Eventually(t, routeTimeout, "the killed collector to restart", func(ctx context.Context) error {
		var restarted corev1.Pod
		if err := p.Get(ctx, &restarted, "-n", namespace, "pod", podName); err != nil {
			return err
		}
		status := restarted.Status.ContainerStatuses[0]
		if status.ContainerID == container || !status.Ready {
			return errors.New("not restarted")
		}
		if status.LastTerminationState.Terminated == nil || status.LastTerminationState.Terminated.ExitCode != 137 {
			return fmt.Errorf("last termination %+v, want exit code 137", status.LastTerminationState.Terminated)
		}
		return nil
	})
	if _, err := p.Command(t.Context(), "docker", "start", p.IngestContainer()); err != nil {
		t.Fatal(err)
	}
	waitCount(t, fmt.Sprintf("all %d acknowledged logs to replay after SIGKILL", crashRecords),
		fmt.Sprintf("SELECT count(DISTINCT Body) FROM otel_logs WHERE startsWith(Body, '%s-')", marker),
		func(count int) bool { return count == crashRecords })
}

var errNotYet = errors.New("not yet")

// observed is the fixture TestObservability follows, the clients reading its telemetry and the
// traffic sent to it.
type observed struct {
	w, second      *platform.Workspace
	dir            string
	file           locofile.File
	host           string
	resourceID     string
	tenant         v1alpha1.ApplicationSpec
	own, other     *rpc.Clients
	traceID        string
	clientSpan     string
	marker         string
	trafficStarted time.Time
}

func traceScope(workspace string) *observabilityv1.TraceScope {
	return &observabilityv1.TraceScope{WorkspaceId: workspace}
}

func (o *observed) logsRequest(workspace string, resources ...string) *observabilityv1.QueryLogsRequest {
	return &observabilityv1.QueryLogsRequest{
		WorkspaceId: workspace,
		ResourceIds: resources,
		StartTime:   timestamppb.New(o.trafficStarted.Add(-queryWindow)),
		EndTime:     timestamppb.Now(),
		Search:      "trace_id=" + o.traceID,
		Limit:       proxyLimit,
	}
}

func (o *observed) metricsRequest(metric string) *observabilityv1.QueryMetricsRequest {
	now := time.Now()
	return &observabilityv1.QueryMetricsRequest{
		WorkspaceId: o.w.ID,
		ResourceIds: []string{o.resourceID},
		MetricName:  metric,
		StartTime: timestamppb.New(
			now.Add(-queryWindow),
		),
		EndTime:         timestamppb.New(now),
		IntervalSeconds: metricInterval,
		Aggregation:     "max",
	}
}

func (o *observed) defaultTelemetry(t *testing.T) {
	w, resourceID := o.w, o.resourceID
	w.WaitReady(t, otelApp)
	platform.Eventually(t, routeTimeout, "the default SDK configuration", func(ctx context.Context) error {
		return telemetryEnvMatches(ctx, w, resourceID, otelApp)
	})
	platform.Eventually(
		t,
		routeTimeout,
		"the fixture to log the injected collector endpoint",
		func(ctx context.Context) error {
			logs, err := p.Kubectl(ctx, "-n", w.Namespace, "logs", "deployment/resource-"+resourceID)
			if err == nil && !strings.Contains(logs, "otlp_endpoint="+p.CollectorEndpoint()) {
				return errors.New("no otlp_endpoint line")
			}
			return err
		},
	)
}

func clusterEventsOnce(t *testing.T) {
	for _, set := range []string{"otel-col-deploy", "otel-col-cluster"} {
		p.MustKubectl(
			t,
			"-n",
			p.CollectorNamespace(),
			"rollout",
			"status",
			"statefulset/"+set,
			"--timeout",
			routeTimeout.String(),
		)
	}
	var gateway appsv1.StatefulSet
	p.MustGet(t, &gateway, "-n", p.CollectorNamespace(), "statefulset", "otel-col-deploy")
	if gateway.Status.ReadyReplicas != ingestReplicas {
		t.Fatalf("OTLP ingestion has %d ready replicas, want %d", gateway.Status.ReadyReplicas, ingestReplicas)
	}
	marker := "obs-event-" + randomHex(6)
	p.MustApply(t, fmt.Sprintf(
		`{"apiVersion": "events.k8s.io/v1", "kind": "Event",
  "metadata": {"name": %q, "namespace": %q}, "eventTime": %q,
  "reportingController": "loco.io/e2e", "reportingInstance": "observability", "action": "CheckCollector",
  "reason": "CollectorIsolation", "note": %q, "type": "Normal",
  "regarding": {"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "otel-col-cluster", "namespace": %q}}`,
		marker,
		p.CollectorNamespace(),
		time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"),
		marker,
		p.CollectorNamespace(),
	))
	p.DeleteOnCleanup(t, "-n", p.CollectorNamespace(), "events.events.k8s.io", marker)
	query := fmt.Sprintf("SELECT count() FROM otel_logs WHERE position(Body, '%s') > 0", marker)
	waitCount(t, "the Kubernetes event in ClickHouse", query, func(count int) bool { return count > 0 })
	// A second copy from the other OTLP replica would land within the flush window.
	ctx, cancel := context.WithTimeout(t.Context(), eventFlushWindow)
	defer cancel()
	var duplicates int
	if err := platform.Poll(ctx, func(ctx context.Context) error {
		count, err := p.ClickHouseCount(ctx, query)
		if err == nil && count > 1 {
			duplicates = count
			return nil
		}
		return errNotYet
	}); err == nil {
		t.Errorf("the Kubernetes event is stored %d times, want once", duplicates)
	}
}

func (o *observed) traffic(t *testing.T) {
	host, traceID, clientSpan, marker := o.host, o.traceID, o.clientSpan, o.marker
	platform.Eventually(t, routeTimeout, "the gateway to route "+host, func(ctx context.Context) error {
		if status, err := gatewayStatus(ctx, host, "/healthz", ""); err != nil || status != http.StatusOK {
			return fmt.Errorf("HTTP %d (%w)", status, err)
		}
		return nil
	})
	o.trafficStarted = time.Now()
	for i := range requestCount {
		traceparent := ""
		if i == 0 {
			traceparent = "00-" + traceID + "-" + clientSpan + "-" + sampledTraceFlags
		}
		path := fmt.Sprintf("/?e2e=%s-%d", marker, i+1)
		if status, err := gatewayStatus(t.Context(), host, path, traceparent); err != nil || status != http.StatusOK {
			t.Errorf("request %d through the gateway: HTTP %d (%v)", i+1, status, err)
		}
	}
	t.Logf(
		"%d requests through the gateway; the first carries trace %s with client span %s",
		requestCount,
		traceID,
		clientSpan,
	)
}

func migrations(t *testing.T) {
	latest := latestMigration(t)
	waitRows(
		t,
		"the latest embedded migration applied",
		"SELECT max(version_id) FROM loco_schema_migrations WHERE is_applied",
		func(rows [][]string) error {
			if len(rows) != 1 || rows[0][0] != strconv.Itoa(latest) {
				return fmt.Errorf("applied %v, want %d", rows, latest)
			}
			return nil
		},
	)
}

func (o *observed) traceSummary(t *testing.T) {
	w, own, traceID, trafficStarted := o.w, o.own, o.traceID, o.trafficStarted
	platform.Eventually(t, telemetryTimeout, "ListTraces to summarize "+traceID, func(ctx context.Context) error {
		listed, err := own.Observability.ListTraces(ctx, &observabilityv1.ListTracesRequest{
			Scope: traceScope(
				w.ID,
			),
			StartTime: timestamppb.New(trafficStarted.Add(-queryWindow)),
			EndTime:   timestamppb.Now(),
			TraceId:   traceID,
			Limit:     1,
		})
		if err != nil {
			return err
		}
		if len(listed.GetSummaries()) != 1 || listed.GetSummaries()[0].GetSpanCount() <= 1 ||
			listed.GetTotalCount() != 1 ||
			listed.GetSummaries()[0].GetTraceId() != traceID {
			return fmt.Errorf("summaries %v, total %d", listed.GetSummaries(), listed.GetTotalCount())
		}
		return nil
	})
}

func (o *observed) ingressTrace(t *testing.T) {
	w, resourceID, own, traceID := o.w, o.resourceID, o.own, o.traceID
	platform.Eventually(
		t,
		telemetryTimeout,
		"an application span whose parent is the gateway's, with the fixture's span link",
		func(ctx context.Context) error {
			trace, err := own.Observability.GetTrace(
				ctx,
				&observabilityv1.GetTraceRequest{Scope: traceScope(w.ID), TraceId: traceID},
			)
			if err != nil {
				return err
			}
			if err := ingressTraceLinked(trace.GetSpans(), w.ID, resourceID, w.OrgID, traceID); err != nil {
				return err
			}
			return fixtureLinkReturned(trace.GetSpans())
		},
	)
}

func (o *observed) appLogs(t *testing.T) {
	w, resourceID, tenant, traceID := o.w, o.resourceID, o.tenant, o.traceID
	waitRows(t, "the fixture's log lines with the trace", fmt.Sprintf(
		"SELECT WorkspaceId, EnvironmentId, ResourceId, OrganizationId FROM otel_logs WHERE Body LIKE '%%trace_id=%s%%'",
		traceID,
	),
		anyRow(w.ID, tenant.EnvironmentID, resourceID, w.OrgID))
}

func (o *observed) accessLogs(t *testing.T) {
	w, resourceID, marker := o.w, o.resourceID, o.marker
	match := fmt.Sprintf(
		"position(Body, 'e2e=%[1]s-') > 0 OR arrayExists(v -> position(v, 'e2e=%[1]s-') > 0, mapValues(LogAttributes))",
		marker,
	)
	waitRows(
		t,
		"access log rows for the requests",
		"SELECT WorkspaceId, ResourceId FROM otel_logs WHERE "+match,
		anyRow(w.ID, resourceID),
	)
}

func (o *observed) envoyMetrics(t *testing.T) {
	w, resourceID, tenant, trafficStarted := o.w, o.resourceID, o.tenant, o.trafficStarted
	union := metricsUnion("MetricName, WorkspaceId, ResourceId, EnvironmentId", trafficStarted)
	waitRows(
		t,
		"an upstream_rq metric of the fixture's route",
		fmt.Sprintf(
			"SELECT MetricName, WorkspaceId, ResourceId, EnvironmentId FROM (%s) WHERE ResourceId = '%s'",
			union,
			resourceID,
		),
		func(rows [][]string) error {
			for _, row := range rows {
				if strings.Contains(row[0], "upstream_rq") && row[1] == w.ID && row[2] == resourceID &&
					row[3] == tenant.EnvironmentID {
					return nil
				}
			}
			return fmt.Errorf("%d metric rows, none upstream_rq of the fixture", len(rows))
		},
	)
}

func (o *observed) forgedAttributes(t *testing.T) {
	w, resourceID := o.w, o.resourceID
	counts := make([]string, 0, 7)
	for table := range strings.FieldsSeq(allTables) {
		counts = append(
			counts,
			fmt.Sprintf(
				"SELECT countIf(WorkspaceId = '%s' OR OrganizationId = '%s') AS ForgedRows FROM %s",
				forgedWorkspace,
				forgedOrganization,
				table,
			),
		)
	}
	waitRows(t, "the forged span with the real tenancy and no forged rows", fmt.Sprintf(
		"SELECT (SELECT sum(ForgedRows) FROM (%s)), "+
			"countIf(OrganizationId = '%s' AND WorkspaceId = '%s' AND ResourceId = '%s'), count() "+
			"FROM otel_traces WHERE SpanName = '%s'",
		strings.Join(counts, " UNION ALL "),
		w.OrgID,
		w.ID,
		resourceID,
		forgedSpan,
	), func(rows [][]string) error {
		if len(rows) != 1 || rows[0][0] != "0" || rows[0][2] == "0" || rows[0][1] != rows[0][2] {
			return fmt.Errorf("forged rows, real and total forged spans: %v", rows)
		}
		return nil
	})
}

func (o *observed) scopedLogs(t *testing.T) {
	w, resourceID, own, traceID := o.w, o.resourceID, o.own, o.traceID
	platform.Eventually(
		t,
		telemetryTimeout,
		"QueryLogs to return the fixture's traced log in its region only",
		func(ctx context.Context) error {
			logs, err := own.Observability.QueryLogs(ctx, o.logsRequest(w.ID, resourceID))
			if err != nil {
				return err
			}
			var region string
			for _, entry := range logs.GetEntries() {
				if entry.GetResourceId() == resourceID && strings.Contains(entry.GetBody(), "trace_id="+traceID) {
					region = entry.GetResourceAttributes()["loco.region"]
				}
			}
			if region == "" {
				return fmt.Errorf(
					"%d entries, none of the fixture with the trace and a region",
					len(logs.GetEntries()),
				)
			}
			included := o.logsRequest(w.ID, resourceID)
			included.Regions = []string{region}
			if logs, err = own.Observability.QueryLogs(ctx, included); err != nil {
				return err
			}
			if len(logs.GetEntries()) == 0 {
				return fmt.Errorf("region %s returned no entries", region)
			}
			excluded := o.logsRequest(w.ID, resourceID)
			excluded.ExcludedRegions = []string{region}
			if logs, err = own.Observability.QueryLogs(ctx, excluded); err != nil {
				return err
			}
			if len(logs.GetEntries()) != 0 {
				return fmt.Errorf("excluding region %s returned %d entries", region, len(logs.GetEntries()))
			}
			return nil
		},
	)
}

func (o *observed) attributeLogs(t *testing.T) {
	excluded := []string{fmt.Sprintf("/?e2e=%s-1", o.marker), fmt.Sprintf("/?e2e=%s-2", o.marker)}
	platform.Eventually(
		t,
		telemetryTimeout,
		"QueryLogs to return the GET access logs except the excluded paths",
		func(ctx context.Context) error {
			request := o.logsRequest(o.w.ID, o.resourceID)
			request.Search = ""
			request.LogAttributes = []*observabilityv1.LogFieldFilter{{Key: "method", Values: []string{http.MethodGet}}}
			request.ExcludedLogAttributes = []*observabilityv1.LogFieldFilter{{Key: "path", Values: excluded}}
			logs, err := o.own.Observability.QueryLogs(ctx, request)
			if err != nil {
				return err
			}
			var marked int
			for _, entry := range logs.GetEntries() {
				attributes := entry.GetLogAttributes()
				if attributes["method"] != http.MethodGet || slices.Contains(excluded, attributes["path"]) {
					return fmt.Errorf("entry outside the filters: %v", attributes)
				}
				if strings.HasPrefix(attributes["path"], "/?e2e="+o.marker+"-") {
					marked++
				}
			}
			if marked != requestCount-len(excluded) {
				return fmt.Errorf("%d access logs of the marked requests, want %d", marked, requestCount-len(excluded))
			}
			return nil
		},
	)
}

func (o *observed) counterMetrics(t *testing.T) {
	w, resourceID, own, other, trafficStarted := o.w, o.resourceID, o.own, o.other, o.trafficStarted
	platform.Eventually(
		t,
		telemetryTimeout,
		"a positive Sum series of the fixture",
		func(ctx context.Context) error {
			rows, err := p.ClickHouse(
				ctx,
				fmt.Sprintf(
					"SELECT MetricName FROM otel_metrics_sum WHERE WorkspaceId = '%s' AND ResourceId = '%s' "+
						"AND TimeUnix >= toDateTime(%d) AND Value > 0 LIMIT 1",
					w.ID,
					resourceID,
					trafficStarted.Unix(),
				),
			)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return errors.New("no positive Sum metric of the fixture")
			}
			series, err := own.Observability.QueryMetrics(ctx, o.metricsRequest(rows[0][0]))
			if err != nil {
				return err
			}
			for _, s := range series.GetSeries() {
				for _, point := range s.GetPoints() {
					if s.GetLabels()["metric_type"] == "sum" && point.GetValue() > 0 {
						_, err := other.Observability.QueryMetrics(ctx, o.metricsRequest(rows[0][0]))
						if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
							t.Errorf(
								"another workspace's token reading the metric: code %v, want permission_denied",
								code,
							)
						}
						return nil
					}
				}
			}
			return fmt.Errorf("no positive Sum points for %s", rows[0][0])
		},
	)
}

func (o *observed) clusterMetrics(t *testing.T) {
	own := o.own
	for _, metric := range []string{"k8s.pod.phase", "k8s.pod.memory.usage"} {
		platform.Eventually(t, telemetryTimeout, metric+" points of the fixture", func(ctx context.Context) error {
			series, err := own.Observability.QueryMetrics(ctx, o.metricsRequest(metric))
			if err != nil {
				return err
			}
			for _, s := range series.GetSeries() {
				if len(s.GetPoints()) > 0 {
					return nil
				}
			}
			return errors.New("no points")
		})
	}
}

func (o *observed) isolation(t *testing.T) {
	w, second, resourceID, own, other, traceID := o.w, o.second, o.resourceID, o.own, o.other, o.traceID
	for _, read := range []struct {
		what   string
		client *rpc.Clients
		scope  string
	}{
		{"the second workspace reading the first", other, w.ID},
		{"the first workspace reading the second", own, second.ID},
	} {
		_, err := read.client.Observability.QueryLogs(t.Context(), o.logsRequest(read.scope))
		if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
			t.Errorf("QueryLogs, %s: code %v, want permission_denied", read.what, code)
		}
		_, err = read.client.Observability.GetTrace(
			t.Context(),
			&observabilityv1.GetTraceRequest{Scope: traceScope(read.scope), TraceId: traceID},
		)
		if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
			t.Errorf("GetTrace, %s: code %v, want permission_denied", read.what, code)
		}
	}
	logs, err := other.Observability.QueryLogs(t.Context(), o.logsRequest(second.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs.GetEntries() {
		if entry.GetResourceId() == resourceID || strings.Contains(entry.GetBody(), traceID) {
			t.Errorf("the second workspace's own query returns the first's entry %v", entry)
		}
	}
	trace, err := other.Observability.GetTrace(
		t.Context(),
		&observabilityv1.GetTraceRequest{Scope: traceScope(second.ID), TraceId: traceID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.GetSpans()) != 0 {
		t.Errorf("the second workspace's own GetTrace returns %d of the first's spans", len(trace.GetSpans()))
	}
}

func (o *observed) userSDKConfiguration(t *testing.T) {
	w, dir, file, host, resourceID := o.w, o.dir, o.file, o.host, o.resourceID
	custom := otelApp + "-custom"
	service := file.Services[otelApp]
	service.Observability = &locofile.Observability{InjectOtel: new(true)}
	service.Env = map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": p.CollectorEndpoint(),
		"OTEL_EXPORTER_OTLP_PROTOCOL": otlpProtobuf,
		"OTEL_SERVICE_NAME":           custom,
	}
	file.Services[otelApp] = service
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "infra", "apply", "--yes")
	platform.Eventually(t, routeTimeout, "the user-owned SDK configuration", func(ctx context.Context) error {
		return telemetryEnvMatches(ctx, w, resourceID, custom)
	})
	p.MustKubectl(
		t,
		"-n",
		w.Namespace,
		"rollout",
		"status",
		"deployment/resource-"+resourceID,
		"--timeout",
		routeTimeout.String(),
	)
	if _, ok := containerEnv(t, w, resourceID)["OTEL_RESOURCE_ATTRIBUTES"]; ok {
		t.Error("a user endpoint keeps the injected resource attributes")
	}
	path := "/telemetry-custom-" + randomHex(6)
	if status, err := gatewayStatus(t.Context(), host, path, ""); err != nil || status != http.StatusOK {
		t.Fatalf("the custom-configured app: HTTP %d (%v)", status, err)
	}
	waitCount(
		t,
		"the custom-named trace",
		fmt.Sprintf(
			"SELECT count() FROM otel_traces WHERE ResourceId = '%s' AND ServiceName = '%s' AND SpanName = 'GET %s'",
			resourceID,
			custom,
			path,
		),
		func(count int) bool { return count > 0 },
	)
}

func (o *observed) injectionDisabled(t *testing.T) {
	w, dir, file, host, resourceID := o.w, o.dir, o.file, o.host, o.resourceID
	service := file.Services[otelApp]
	service.Observability = &locofile.Observability{InjectOtel: new(false)}
	service.Env = nil
	file.Services[otelApp] = service
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "infra", "apply", "--yes")
	p.MustKubectl(
		t,
		"-n",
		w.Namespace,
		"rollout",
		"status",
		"deployment/resource-"+resourceID,
		"--timeout",
		routeTimeout.String(),
	)
	for name := range containerEnv(t, w, resourceID) {
		if strings.HasPrefix(name, "OTEL_") {
			t.Errorf("injectOtel false injects %s", name)
		}
	}
	path := "/injection-disabled-" + randomHex(6)
	if status, err := gatewayStatus(t.Context(), host, path, ""); err != nil || status != http.StatusOK {
		t.Fatalf("the app without SDK configuration: HTTP %d (%v)", status, err)
	}
	waitCount(
		t,
		"the fixture's stdout log",
		fmt.Sprintf(
			"SELECT count() FROM otel_logs WHERE ResourceId = '%s' AND position(Body, 'msg=request') > 0 "+
				"AND position(Body, 'path=%s') > 0",
			resourceID,
			path,
		),
		func(count int) bool { return count > 0 },
	)
}

// restoreCollector starts the ingest container again and rolls the collector back to its
// configuration.
func restoreCollector(t *testing.T, original *corev1.ConfigMap) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	if _, err := p.Command(ctx, "docker", "start", p.IngestContainer()); err != nil {
		t.Error(err)
	}
	restored, err := json.Marshal(map[string]any{"data": original.Data})
	if err != nil {
		t.Error(err)
		return
	}
	namespace, collector := original.Namespace, original.Name
	for _, args := range [][]string{
		{"-n", namespace, "patch", "configmap", collector, "--type", "merge", "-p", string(restored)},
		{"-n", namespace, "rollout", "restart", "statefulset/" + collector},
		{"-n", namespace, "rollout", "status", "statefulset/" + collector, "--timeout", routeTimeout.String()},
	} {
		if _, err := p.Kubectl(ctx, args...); err != nil {
			t.Error(err)
		}
	}
}
