package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	testMaxRequestBytes = 1 << 16
	testBufferBytes     = 1 << 20
	testLogBody         = "hello"
)

var errStorageDown = errors.New("clickhouse: connection refused")

type recordingLogs struct {
	err      error
	received []plog.Logs
}

func (r *recordingLogs) consume(_ context.Context, logs plog.Logs) error {
	r.received = append(r.received, logs)
	return r.err
}

func newTestService(t *testing.T, logsErr error) (*Service, *recordingLogs) {
	t.Helper()
	recorder := &recordingLogs{err: logsErr}
	logs, err := consumer.NewLogs(recorder.consume)
	if err != nil {
		t.Fatalf("logs consumer: %v", err)
	}
	traces, err := consumer.NewTraces(func(context.Context, ptrace.Traces) error { return nil })
	if err != nil {
		t.Fatalf("traces consumer: %v", err)
	}
	metrics, err := consumer.NewMetrics(func(context.Context, pmetric.Metrics) error { return nil })
	if err != nil {
		t.Fatalf("metrics consumer: %v", err)
	}
	return NewService(Consumers{Logs: logs, Traces: traces, Metrics: metrics}, testMaxRequestBytes), recorder
}

func testLogsRequest() plogotlp.ExportRequest {
	logs := plog.NewLogs()
	record := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.Body().SetStr(testLogBody)
	return plogotlp.NewExportRequestFromLogs(logs)
}

func protoBody(t *testing.T) []byte {
	t.Helper()
	request := testLogsRequest()
	body, err := request.MarshalProto()
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return body
}

func post(t *testing.T, service *Service, contentType, encoding string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, LogsPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	if encoding != "" {
		request.Header.Set("Content-Encoding", encoding)
	}
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	return response
}

func decodeStatus(t *testing.T, response *httptest.ResponseRecorder) *spb.Status {
	t.Helper()
	message := &spb.Status{}
	body := response.Body.Bytes()
	var err error
	if response.Header().Get("Content-Type") == jsonContentType {
		err = protojson.Unmarshal(body, message)
	} else {
		err = proto.Unmarshal(body, message)
	}
	if err != nil {
		t.Fatalf("decode status body %q: %v", body, err)
	}
	return message
}

func TestHTTPExportAcceptsProtobufJSONAndGzip(t *testing.T) {
	service, recorder := newTestService(t, nil)
	request := testLogsRequest()
	jsonBody, err := request.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(protoBody(t)); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	for _, test := range []struct {
		name        string
		contentType string
		encoding    string
		body        []byte
	}{
		{name: "protobuf", contentType: protobufContentType, body: protoBody(t)},
		{name: "json", contentType: jsonContentType + "; charset=utf-8", body: jsonBody},
		{name: "gzip", contentType: protobufContentType, encoding: gzipEncoding, body: compressed.Bytes()},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := post(t, service, test.contentType, test.encoding, test.body)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body %q", response.Code, response.Body.String())
			}
			want := protobufContentType
			if test.name == "json" {
				want = jsonContentType
			}
			if got := response.Header().Get("Content-Type"); got != want {
				t.Fatalf("content type = %q, want %q", got, want)
			}
		})
	}
	if len(recorder.received) != 3 {
		t.Fatalf("consumer received %d requests, want 3", len(recorder.received))
	}
	for _, logs := range recorder.received {
		body := logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Str()
		if body != testLogBody {
			t.Fatalf("body = %q, want %q", body, testLogBody)
		}
	}
}

func TestHTTPExportStatusCodes(t *testing.T) {
	for _, test := range []struct {
		name        string
		consumerErr error
		contentType string
		encoding    string
		body        []byte
		wantHTTP    int
		wantCode    codes.Code
	}{
		{
			name: "storage unavailable is retryable", consumerErr: errStorageDown,
			contentType: protobufContentType, wantHTTP: http.StatusServiceUnavailable, wantCode: codes.Unavailable,
		},
		{
			name: "permanent consumer error is not retryable", consumerErr: consumererror.NewPermanent(errStorageDown),
			contentType: protobufContentType, wantHTTP: http.StatusBadRequest, wantCode: codes.InvalidArgument,
		},
		{
			name: "undecodable body", contentType: protobufContentType, body: []byte{0xff, 0xff},
			wantHTTP: http.StatusBadRequest, wantCode: codes.InvalidArgument,
		},
		{
			name: "undecodable json", contentType: jsonContentType, body: []byte("{"),
			wantHTTP: http.StatusBadRequest, wantCode: codes.InvalidArgument,
		},
		{
			name: "unsupported content type", contentType: "text/plain",
			wantHTTP: http.StatusUnsupportedMediaType, wantCode: codes.InvalidArgument,
		},
		{
			name: "unsupported encoding", contentType: protobufContentType, encoding: "br",
			wantHTTP: http.StatusBadRequest, wantCode: codes.InvalidArgument,
		},
		{
			name: "oversized body", contentType: protobufContentType, body: make([]byte, testMaxRequestBytes+1),
			wantHTTP: http.StatusRequestEntityTooLarge, wantCode: codes.InvalidArgument,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newTestService(t, test.consumerErr)
			body := test.body
			if body == nil {
				body = protoBody(t)
			}
			response := post(t, service, test.contentType, test.encoding, body)
			if response.Code != test.wantHTTP {
				t.Fatalf("status = %d, want %d", response.Code, test.wantHTTP)
			}
			message := decodeStatus(t, response)
			if codes.Code(message.GetCode()) != test.wantCode {
				t.Fatalf("status code = %s, want %s", codes.Code(message.GetCode()), test.wantCode)
			}
			if bytes.Contains(response.Body.Bytes(), []byte(errStorageDown.Error())) {
				t.Fatalf("response exposes the storage error: %q", response.Body.String())
			}
		})
	}
}

func TestHTTPExportRejectsOtherMethods(t *testing.T) {
	service, _ := newTestService(t, nil)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, LogsPath, nil)
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func grpcClient(t *testing.T, service *Service) plogotlp.GRPCClient {
	t.Helper()
	listener := bufconn.Listen(testBufferBytes)
	server := service.GRPCServer()
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(server.Stop)
	dial := func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dial),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close conn: %v", err)
		}
	})
	return plogotlp.NewGRPCClient(conn)
}

func TestGRPCExportStatusCodes(t *testing.T) {
	for _, test := range []struct {
		name        string
		consumerErr error
		want        codes.Code
	}{
		{name: "accepted", want: codes.OK},
		{name: "storage unavailable is retryable", consumerErr: errStorageDown, want: codes.Unavailable},
		{
			name: "permanent consumer error is not retryable", consumerErr: consumererror.NewPermanent(errStorageDown),
			want: codes.InvalidArgument,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, recorder := newTestService(t, test.consumerErr)
			client := grpcClient(t, service)
			_, err := client.Export(t.Context(), testLogsRequest())
			if got := status.Code(err); got != test.want {
				t.Fatalf("code = %s, want %s (%v)", got, test.want, err)
			}
			if len(recorder.received) != 1 {
				t.Fatalf("consumer received %d requests, want 1", len(recorder.received))
			}
			if err != nil && strings.Contains(err.Error(), errStorageDown.Error()) {
				t.Fatalf("error exposes the storage error: %v", err)
			}
		})
	}
}

func TestGRPCRejectsOversizedMessages(t *testing.T) {
	service, recorder := newTestService(t, nil)
	client := grpcClient(t, service)
	logs := plog.NewLogs()
	record := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	oversized := strings.Repeat("a", testMaxRequestBytes+1)
	record.Body().SetStr(oversized)
	request := plogotlp.NewExportRequestFromLogs(logs)
	_, err := client.Export(t.Context(), request)
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Fatalf("code = %s, want %s", got, codes.ResourceExhausted)
	}
	if len(recorder.received) != 0 {
		t.Fatalf("consumer received %d requests, want 0", len(recorder.received))
	}
}
