package main

import (
	otelprom "go.opentelemetry.io/contrib/bridges/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const metricsOff = "0"

// serveMetrics serves the controller-runtime Prometheus metrics on addr, with client-go request
// and rate-limiter latency added; "0" serves none.
func serveMetrics(addr string) metricsserver.Options {
	if addr != metricsOff {
		metrics.RegisterRESTClientMetrics(metrics.MetricRequestLatency, metrics.MetricRateLimiterLatency)
	}
	return metricsserver.Options{BindAddress: addr}
}

// registryMetrics reads the controller-runtime registry the metrics endpoint serves, for OTLP export.
func registryMetrics() sdkmetric.Producer {
	return otelprom.NewMetricProducer(otelprom.WithGatherer(metrics.Registry))
}
