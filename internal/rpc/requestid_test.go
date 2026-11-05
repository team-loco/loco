package rpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const testRequestID = "req-123"

type failingConfigServer struct {
	configv1connect.UnimplementedConfigServiceHandler
}

func (*failingConfigServer) GetConfig(
	ctx context.Context,
	_ *configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	info.ResponseHeader().Set("X-Loco-Request-Id", testRequestID)
	return nil, connect.NewError(connect.CodeFailedPrecondition, "not ready")
}

func TestFailedCallLogsRequestIDOnce(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	server := connect.NewServer()
	configv1connect.RegisterConfigServiceHandler(server, &failingConfigServer{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	clients := rpc.NewHTTPClients(srv.Client(), srv.URL, "")
	if _, err := clients.Config.GetConfig(t.Context(), &configv1.GetConfigRequest{}); err == nil {
		t.Fatal("GetConfig succeeded, want an error")
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("logged %d records, want 1:\n%s", len(lines), logs.String())
	}
	var record struct {
		Level     string `json:"level"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode log record: %v", err)
	}
	if record.Level != slog.LevelDebug.String() || record.RequestID != testRequestID {
		t.Fatalf("record = %+v, want DEBUG with request_id %q", record, testRequestID)
	}
}
