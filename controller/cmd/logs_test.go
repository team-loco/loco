package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
)

func TestConfiguredFrameworkLoggerWritesItsDiagnostic(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	defer slog.SetDefault(previous)
	logger := ctrl.Log.WithName("application")
	configureLogging(&output, slog.LevelInfo)
	logger.Info("manager ready", "controller", "application")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["msg"] != "manager ready" || record["controller"] != "application" {
		t.Fatalf("framework diagnostic missing from stdout: %s", &output)
	}
}
