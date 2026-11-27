package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/team-loco/loco/internal/otelsetup"
)

func TestConfiguredFrameworkLoggerExportsItsDiagnostic(t *testing.T) {
	requests := make(chan *collectorlog.ExportLogsServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		request := &collectorlog.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- request
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	t.Setenv(envOTLPLogsEndpoint, server.URL+"/v1/logs")
	previous := slog.Default()
	defer slog.SetDefault(previous)
	logger := ctrl.Log.WithName("application")
	provider, err := configureLogging(t.Context(), "loco-controller", "test", slog.LevelInfo, true)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("manager ready", "controller", "application")
	if err := otelsetup.StopLogs(provider); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		var found bool
		for _, resource := range request.GetResourceLogs() {
			for _, scope := range resource.GetScopeLogs() {
				for _, record := range scope.GetLogRecords() {
					found = found || record.GetBody().GetStringValue() == "manager ready"
				}
			}
		}
		if !found {
			t.Fatal("framework diagnostic missing from OTLP")
		}
	case <-time.After(time.Second):
		t.Fatal("framework diagnostic did not reach OTLP")
	}
}
