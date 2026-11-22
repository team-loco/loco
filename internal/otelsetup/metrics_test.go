package otelsetup

import (
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

var runtimeMetrics = []string{"go.goroutine.count", "go.schedule.duration"}

func TestMeterProviderRecordsGoRuntimeMetrics(t *testing.T) {
	t.Setenv(serviceNameEnv, "")
	t.Setenv(resourceAttrEnv, "")
	reader := sdkmetric.NewManualReader(runtimeProducer())
	provider, err := newMeterProvider(t.Context(), reader, testServiceName, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := StopMetrics(provider); stopErr != nil {
			t.Error(stopErr)
		}
	})

	var collected metricdata.ResourceMetrics
	if err = reader.Collect(t.Context(), &collected); err != nil {
		t.Fatal(err)
	}
	if got := resourceValue(t, collected.Resource, string(semconv.ServiceNameKey)); got != testServiceName {
		t.Errorf("service.name = %q, want %q", got, testServiceName)
	}
	names := map[string]bool{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			names[m.Name] = true
		}
	}
	for _, name := range runtimeMetrics {
		if !names[name] {
			t.Errorf("collected metrics lack %s", name)
		}
	}
}
