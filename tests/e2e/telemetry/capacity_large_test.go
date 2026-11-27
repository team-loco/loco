package telemetry

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/internal/capacitymetrics"
	"github.com/team-loco/loco/internal/systemtenant"
	"github.com/team-loco/loco/tests/e2e/platform"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// This injects cluster-wide synthetic capacity. Run before parallel telemetry
// tests, with old timestamps so the live controller remains the current source.
func TestCapacityLargeCollectionSplitsBeforeRealIngestion(t *testing.T) {
	const pods = 100000
	source := uuid.NewString()
	captured := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	var token string
	platform.Eventually(t, ingestTokenTimeout, "cluster ingest token", func(ctx context.Context) error {
		var err error
		token, err = p.IngestToken(ctx, p.CollectorNamespace())
		return err
	})
	stats := &capacityExportStats{}
	snapshot := capacityScaleSnapshot(t, source, captured, pods)
	forward := capacityScaleForwarder(t, stats)
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == capacitymetrics.Path {
			snapshot(w, r)
			return
		}
		forward(w, r)
	})

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(proxy)
	server.Listener = listener
	server.Start()
	defer server.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://host.docker.internal:" + port
	var cm corev1.ConfigMap
	p.MustGet(t, &cm, "-n", p.CollectorNamespace(), "configmap", "otel-col-cluster")
	var config capacityCollectorConfig
	if err = yaml.Unmarshal([]byte(cm.Data["relay"]), &config); err != nil {
		t.Fatal(err)
	}
	var collector appsv1.StatefulSet
	p.MustGet(t, &collector, "-n", p.CollectorNamespace(), "statefulset", "otel-col-cluster")
	image := collector.Spec.Template.Spec.Containers[0].Image
	pipeline := config.Service.Pipelines["metrics/capacity"]
	exporterName := pipeline.Exporters[0]
	exporter := config.Exporters[exporterName]
	delete(exporter, "auth")
	exporter["endpoint"] = endpoint
	exporter["headers"] = map[string]any{"Authorization": "Bearer " + token}
	config.Exporters = map[string]map[string]any{exporterName: exporter}
	config.Receivers = map[string]any{"prometheus/capacity": map[string]any{
		"config": map[string]any{"scrape_configs": []any{map[string]any{
			"job_name": "capacity-scale-proof", "scrape_interval": "30s", "scrape_timeout": "30s",
			"metrics_path": capacitymetrics.Path, "honor_timestamps": true,
			"static_configs": []any{map[string]any{"targets": []string{"host.docker.internal:" + port}}},
		}}},
	}}
	queue, ok := exporter["sending_queue"].(map[string]any)
	if !ok {
		t.Fatal("rendered exporter has no sending queue")
	}
	storage, ok := queue["storage"].(string)
	if !ok {
		t.Fatal("rendered queue has no persistent storage identity")
	}
	config.Receivers["otlp"] = map[string]any{
		"protocols": map[string]any{"http": map[string]any{"endpoint": "0.0.0.0:4318"}},
	}
	config.Extensions = map[string]any{
		storage: map[string]any{"directory": "/data/queue", "create_directory": true},
	}
	config.Service.Extensions = []string{storage}
	config.Service.Pipelines = map[string]capacityCollectorPipeline{
		"metrics/capacity": pipeline,
		"logs/platform": {
			Receivers:  []string{"otlp"},
			Processors: pipeline.Processors,
			Exporters:  pipeline.Exporters,
		},
	}
	dir := t.TempDir()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	name := "codex-capacity-scale-" + platform.RandomHex(4)
	if _, err = p.Command(
		t.Context(),
		"docker",
		"run",
		"-d",
		"--name",
		name,
		"--add-host",
		"host.docker.internal:host-gateway",
		"--cpus",
		"2",
		"--memory",
		"2g",
		"--publish",
		"127.0.0.1::4318",
		"--user",
		strconv.Itoa(os.Getuid()),
		"-v",
		dir+":/data",
		image,
		"--config=/data/config.json",
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, removeErr := p.Command(context.Background(), "docker", "rm", "-f", name); removeErr != nil {
			t.Error(removeErr)
		}
	})
	platform.Eventually(
		t,
		3*time.Minute,
		"100,000 synthetic pod reservations reach real ClickHouse",
		func(ctx context.Context) error {
			state, stateErr := p.Command(ctx, "docker", "inspect", "-f", "{{.State.Status}}", name)
			if stateErr != nil {
				return stateErr
			}
			if strings.TrimSpace(state) != "running" {
				logs, logsErr := p.Command(ctx, "docker", "logs", name)
				if logsErr != nil {
					t.Fatal(logsErr)
				}
				t.Fatalf("collector exited: %s", logs)
			}
			rows, queryErr := p.ClickHouse(
				ctx,
				fmt.Sprintf(
					`SELECT uniqExactIf(Attributes['pod_uid'],MetricName='%s'),
uniqExactIf(Attributes['pod_uid'],MetricName='%s'),maxIf(Value,MetricName='%s')
FROM otel_metrics_gauge WHERE WorkspaceId='%s' AND ResourceAttributes['loco.cluster.id']='%s'
AND Attributes['source_id']='%s' AND bitAnd(Flags,1)=0`,
					capacitymetrics.CPU,
					capacitymetrics.Memory,
					capacitymetrics.Pods,
					systemtenant.WorkspaceID,
					p.ClusterID,
					source,
				),
			)
			if queryErr != nil {
				return queryErr
			}
			if len(rows) != 1 || strings.Join(rows[0], ",") != "100000,100000,100000" {
				return fmt.Errorf("incomplete collection: %v", rows)
			}
			return nil
		},
	)
	// A byte cap must preserve individual records that the existing ingest limit accepts.
	portOutput, portErr := p.Command(t.Context(), "docker", "port", name, "4318/tcp")
	if portErr != nil {
		t.Fatal(portErr)
	}
	logs := plog.NewLogs()
	resource := logs.ResourceLogs().AppendEmpty()
	resource.Resource().Attributes().PutStr("loco.telemetry.scope", "platform")
	resource.Resource().Attributes().PutStr("e2e.capacity.source", source)
	record := resource.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
	const logBytes = 7 << 19
	record.Body().SetStr(strings.Repeat("x", logBytes))
	payload, encodeErr := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	response := do(
		t,
		t.Context(),
		http.MethodPost,
		"http://"+strings.TrimSpace(portOutput)+"/v1/logs",
		map[string]string{contentType: protobufContentType},
		payload,
	)
	if response.status != http.StatusOK {
		t.Fatalf("valid large log not accepted by collector: %d", response.status)
	}
	platform.Eventually(
		t,
		telemetryTimeout,
		"valid 3.5MiB log survives export batching",
		func(ctx context.Context) error {
			count, queryErr := p.ClickHouseCount(ctx, fmt.Sprintf(`SELECT count() FROM otel_logs
WHERE WorkspaceId='%s' AND ResourceAttributes['e2e.capacity.source']='%s' AND length(Body)=%d`,
				systemtenant.WorkspaceID, source, logBytes))
			if queryErr != nil {
				return queryErr
			}
			if count < 1 {
				return fmt.Errorf("large log count=%d", count)
			}
			return nil
		},
	)
	stats.Lock()
	defer stats.Unlock()
	if len(stats.rejected) > 0 {
		t.Fatalf("export requests rejected: %v", stats.rejected)
	}
	if len(stats.sizes) < 2 {
		t.Fatalf("large collection was not split: %v", stats.sizes)
	}
	maxBytes := 0
	for _, size := range stats.sizes {
		maxBytes = max(maxBytes, size)
	}
	t.Logf(
		"Synthetic 100,000-pod scrape reaches ClickHouse with 200,000 request points + manifest; "+
			"%d accepted exports; largest decompressed body %d bytes; collector budget 2CPU/2GiB",
		len(stats.sizes),
		maxBytes,
	)
}

type capacityCollectorPipeline struct {
	Receivers  []string `json:"receivers"`
	Processors []string `json:"processors"`
	Exporters  []string `json:"exporters"`
}
type capacityCollectorConfig struct {
	Receivers  map[string]any            `json:"receivers"`
	Processors map[string]any            `json:"processors"`
	Exporters  map[string]map[string]any `json:"exporters"`
	Extensions map[string]any            `json:"extensions"`
	Service    struct {
		Pipelines  map[string]capacityCollectorPipeline `json:"pipelines"`
		Extensions []string                             `json:"extensions"`
	} `json:"service"`
}

func capacityScaleSnapshot(t *testing.T, source string, captured time.Time, pods int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		out := bufio.NewWriter(w)
		for _, name := range []string{capacitymetrics.CPU, capacitymetrics.Memory, capacitymetrics.Pods} {
			if _, err := fmt.Fprintf(out, "# TYPE %s gauge\n", name); err != nil {
				return
			}
		}
		for i := range pods {
			attrs := fmt.Sprintf(
				`source_id=%q,node="scale-node-%d",pod_uid="%08x-0000-0000-0000-%012x",`+
					`workspace_id="11111111-2222-3333-4444-555555555555",resource_id="%08x-1111-1111-1111-%012x"`,
				source,
				i%1000,
				i,
				i,
				i,
				i,
			)
			if _, err := fmt.Fprintf(
				out,
				"%s{%s} 0.2 %d\n%s{%s} 134217728 %d\n",
				capacitymetrics.CPU,
				attrs,
				captured.UnixMilli(),
				capacitymetrics.Memory,
				attrs,
				captured.UnixMilli(),
			); err != nil {
				return
			}
		}
		if _, err := fmt.Fprintf(
			out,
			"%s{source_id=%q} %d %d\n",
			capacitymetrics.Pods,
			source,
			pods,
			captured.UnixMilli(),
		); err != nil {
			return
		}
		if err := out.Flush(); err != nil {
			t.Error(err)
		}
	}
}

type capacityExportStats struct {
	sync.Mutex
	sizes    []int
	rejected []int
}

func capacityScaleForwarder(t *testing.T, stats *capacityExportStats) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		decompressed := payload
		if r.Header.Get("Content-Encoding") == "gzip" {
			reader, decodeErr := gzip.NewReader(bytes.NewReader(payload))
			if decodeErr != nil {
				t.Error(decodeErr)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			decompressed, err = io.ReadAll(reader)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err = reader.Close(); err != nil {
				t.Error(err)
			}
		}
		request, err := http.NewRequestWithContext(
			r.Context(),
			http.MethodPost,
			p.OTLPURL+capacityExportPath(r.URL.Path),
			bytes.NewReader(payload),
		)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		request.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		stats.Lock()
		stats.sizes = append(stats.sizes, len(decompressed))
		if response.StatusCode != http.StatusOK {
			stats.rejected = append(stats.rejected, response.StatusCode)
		}
		stats.Unlock()
		maps.Copy(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		if _, err = io.Copy(w, response.Body); err != nil {
			t.Error(err)
		}
	}
}

func capacityExportPath(path string) string {
	if path == "/v1/logs" {
		return "/v1/logs"
	}
	return "/v1/metrics"
}
