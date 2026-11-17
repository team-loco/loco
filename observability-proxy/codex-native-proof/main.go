package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
)

//nolint:gocyclo,gosec // Single-use isolated diagnostic; image is resolved from the pinned upstream release.
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		panic("listener has no TCP address")
	}
	port := address.Port
	received := make(chan struct{}, 1)
	var requested atomic.Bool
	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, readErr := io.ReadAll(io.LimitReader(r.Body, 10<<20))
			if readErr != nil {
				panic(readErr)
			}
			request := pmetricotlp.NewExportRequest()
			if decodeErr := request.UnmarshalProto(b); decodeErr != nil {
				panic(decodeErr)
			}
			resources := request.Metrics().ResourceMetrics()
			for i := range resources.Len() {
				scopes := resources.At(i).ScopeMetrics()
				for j := range scopes.Len() {
					metrics := scopes.At(j).Metrics()
					for k := range metrics.Len() {
						metric := metrics.At(k)
						if !requested.Load() || !strings.Contains(metric.Name(), "downstream_rq_total") ||
							metric.Type() != pmetric.MetricTypeSum ||
							metric.Sum().DataPoints().Len() == 0 {
							continue
						}
						point := metric.Sum().DataPoints().At(0)
						if point.StartTimestamp() == 0 || point.Timestamp() == 0 ||
							(point.IntValue() == 0 && point.DoubleValue() == 0) {
							continue
						}
						fmt.Printf(
							"TIMESTAMP_BOUNDS sample_within_minute=%t raw_start_within_minute=%t "+
								"start_times_1000_within_minute=%t\n",
							time.Since(point.Timestamp().AsTime()).Abs() < time.Minute,
							time.Since(point.StartTimestamp().AsTime()).Abs() < time.Minute,
							time.Since(time.Unix(0, int64(point.StartTimestamp())*1000)).Abs() < time.Minute,
						)
						fmt.Printf(
							"START_SAMPLE_RATIO %.6f MONOTONIC %t TEMPORALITY %s\n",
							float64(point.Timestamp())/float64(point.StartTimestamp()),
							metric.Sum().IsMonotonic(),
							metric.Sum().AggregationTemporality(),
						)
						fmt.Printf(
							"RAW_OTLP metric=%s start=%d (%s) sample=%d (%s)\n",
							metric.Name(),
							uint64(point.StartTimestamp()),
							point.StartTimestamp().AsTime().UTC().Format(time.RFC3339Nano),
							uint64(point.Timestamp()),
							point.Timestamp().AsTime().UTC().Format(time.RFC3339Nano),
						)
						select {
						case received <- struct{}{}:
						default:
						}
						break
					}
				}
			}
			w.Header().Set("Content-Type", "application/x-protobuf")
			w.WriteHeader(http.StatusOK)
		}),
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			panic(serveErr)
		}
	}()
	defer server.Close()
	config := fmt.Sprintf(`admin:
  address:
    socket_address: {address: 0.0.0.0, port_value: 9901}
static_resources:
  listeners:
  - name: proof
    address:
      socket_address: {address: 0.0.0.0, port_value: 10000}
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          '@type': type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: proof
          route_config:
            name: proof
            virtual_hosts:
            - name: proof
              domains: ['*']
              routes:
              - match: {prefix: /}
                direct_response: {status: 200, body: {inline_string: proof}}
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              '@type': type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  clusters:
  - name: otel
    type: STRICT_DNS
    connect_timeout: 1s
    load_assignment:
      cluster_name: otel
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: {address: 127.0.0.1, port_value: %d}
stats_flush_interval: 1s
stats_sinks:
- name: envoy.stat_sinks.open_telemetry
  typed_config:
    '@type': type.googleapis.com/envoy.extensions.stat_sinks.open_telemetry.v3.SinkConfig
    http_service:
      http_uri:
        uri: http://127.0.0.1:%d/v1/metrics
        cluster: otel
        timeout: 1s
`, port, port)
	dir, err := os.MkdirTemp("", "codex-envoy-start-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "envoy.yaml")
	//nolint:gosec // Public diagnostic configuration read by the distroless Envoy user.
	if err = os.WriteFile(path, []byte(config), 0644); err != nil {
		panic(err)
	}
	image := os.Getenv("CODEX_ENVOY_IMAGE")
	if image == "" {
		panic("CODEX_ENVOY_IMAGE required")
	}
	fmt.Println("IMAGE", image, "PROCESS_START_LOWER_BOUND", time.Now().UTC().Format(time.RFC3339Nano))
	cmd := exec.CommandContext(
		ctx,
		"docker",
		"run",
		"--rm",
		"--name",
		"codex-envoy-start-proof",
		"--network",
		"host",
		"-v",
		path+":/etc/envoy/envoy.yaml:ro",
		image,
		"-c",
		"/etc/envoy/envoy.yaml",
		"--concurrency",
		"1",
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		output, err := exec.CommandContext(cleanupCtx, "docker", "stop", "codex-envoy-start-proof").CombinedOutput()
		fmt.Printf("STOP %s %v\n", output, err)
		fmt.Println("ENVOY_EXIT", cmd.Wait())
	}()
	client := &http.Client{Timeout: time.Second}
	for {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:10000/", nil)
		if requestErr != nil {
			panic(requestErr)
		}
		response, err := client.Do(request)
		if err == nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				panic(closeErr)
			}
			if response.StatusCode != http.StatusOK {
				panic(fmt.Sprintf("request status %d", response.StatusCode))
			}
			requested.Store(true)
			fmt.Println("HTTP_REQUEST_OK", time.Now().UTC().Format(time.RFC3339Nano))
			break
		}
		select {
		case <-ctx.Done():
			panic(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	select {
	case <-received:
	case <-ctx.Done():
		panic(ctx.Err())
	}
}
