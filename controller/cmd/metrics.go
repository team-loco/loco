package main

import (
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
