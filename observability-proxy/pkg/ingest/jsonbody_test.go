package ingest

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
)

const (
	jsonBodyMessageKey = "msg"
	quotedLogMessage   = `said "hello"`
	quotedLogText      = `say "hello" from C:\app`
	quotedJSONString   = `"hello"`
	quotedJSONObject   = `{"msg":"said \"hello\"","user":{"id":"42"}}`
)

func TestExportedLogsCarryTheirJSONBodyFieldsAsAttributes(t *testing.T) {
	manyFields := make([]string, 0, maxBodyFields+1)
	for i := range maxBodyFields + 1 {
		manyFields = append(manyFields, fmt.Sprintf(`"f%03d":%d`, i, i))
	}
	for _, tc := range []struct {
		name     string
		body     any
		existing map[string]string
		want     map[string]string
	}{
		{
			name: "nested object, array, number, bool and null",
			body: ` {"msg":"paid","user":{"id":"42","plan":"pro"},"roles":["admin","dev"],"amount":12.50,"ok":true,"x":null}`,
			want: map[string]string{
				jsonBodyMessageKey: "paid", "user.id": "42", "user.plan": "pro", "roles.0": "admin", "roles.1": "dev",
				"amount": "12.50", "ok": "true", "x": "null",
			},
		},
		{
			name:     "existing attributes win",
			body:     `{"method":"POST","path":"/api"}`,
			existing: map[string]string{"method": "GET"},
			want:     map[string]string{"method": "GET", "path": "/api"},
		},
		{
			name: "long keys are skipped",
			body: `{"` + strings.Repeat("k", maxBodyFieldKey+1) + `":"v","short":"v"}`,
			want: map[string]string{"short": "v"},
		},
		{
			name: "colliding paths keep the first in key order",
			body: `{"a.b":"literal","a":{"b":"nested"}}`,
			want: map[string]string{"a.b": "nested"},
		},
		{name: "plain text", body: "GET /healthz 200", want: map[string]string{}},
		{name: "JSON array", body: `[{"a":1}]`, want: map[string]string{}},
		{name: "two objects", body: `{"a":1} {"b":2}`, want: map[string]string{}},
		{name: "missing closing brace", body: `{"a":1`, want: map[string]string{}},
		{name: "extra closing brace", body: `{"a":1}}`, want: map[string]string{}},
		{name: "trailing closing bracket", body: `{"a":1}]`, want: map[string]string{}},
		{name: "duplicate object names", body: `{"a":1,"a":2}`, want: map[string]string{}},
		{name: "multi-line object", body: "{\"a\":1,\n\"b\":2}", want: map[string]string{"a": "1", "b": "2"}},
		{
			name: "structured OTLP object",
			body: map[string]any{
				jsonBodyMessageKey: quotedLogMessage, "user": map[string]any{"id": "42"},
				"roles": []any{"operator"}, "ok": true, "count": int64(9007199254740993), "x": nil,
			},
			want: map[string]string{
				jsonBodyMessageKey: quotedLogMessage, "user.id": "42", "roles.0": "operator",
				"ok": "true", "count": "9007199254740993", "x": "null",
			},
		},
		{name: "quoted text", body: quotedLogText, want: map[string]string{}},
		{name: "JSON string literal", body: quotedJSONString, want: map[string]string{}},
		{name: "quoted JSON string", body: `"{\"msg\":\"hello\"}"`, want: map[string]string{}},
		{
			name: "quoted JSON field",
			body: `{"msg":"said \"hello\"","path":"C:\\app"}`,
			want: map[string]string{jsonBodyMessageKey: quotedLogMessage, "path": `C:\app`},
		},
		{name: "invalid JSON", body: `{"a":`, want: map[string]string{}},
		{name: "empty object", body: `{}`, want: map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attributes := exportLog(t, tc.body, tc.existing)
			if !maps.Equal(attributes, tc.want) {
				t.Fatalf("attributes = %v, want %v", attributes, tc.want)
			}
		})
	}
	t.Run("field count is capped", func(t *testing.T) {
		attributes := exportLog(t, "{"+strings.Join(manyFields, ",")+"}", nil)
		if len(attributes) != maxBodyFields {
			t.Fatalf("%d attributes, want %d", len(attributes), maxBodyFields)
		}
	})
}

const (
	methodKey       = "http.request.method"
	legacyMethodKey = "http.method"
	shortMethodKey  = "method"
)

func TestExportedLogsCarryTheHTTPMethodAndStatusCodeUnderShortNames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		existing map[string]string
		want     map[string]string
	}{
		{
			name:     "semantic convention attributes",
			existing: map[string]string{methodKey: http.MethodGet, "http.response.status_code": "200"},
			want: map[string]string{
				methodKey: http.MethodGet, "http.response.status_code": "200", shortMethodKey: http.MethodGet, "status_code": "200",
			},
		},
		{
			name: "deprecated attributes from a JSON body",
			body: `{"http":{"method":"POST","status_code":503}}`,
			want: map[string]string{
				legacyMethodKey: http.MethodPost, "http.status_code": "503", shortMethodKey: http.MethodPost, "status_code": "503",
			},
		},
		{
			name:     "the current name wins over the deprecated one",
			existing: map[string]string{methodKey: http.MethodPut, legacyMethodKey: http.MethodGet},
			want: map[string]string{
				methodKey: http.MethodPut, legacyMethodKey: http.MethodGet, shortMethodKey: http.MethodPut,
			},
		},
		{
			name:     "an attribute with the short name is kept",
			existing: map[string]string{methodKey: http.MethodGet, shortMethodKey: "custom"},
			want:     map[string]string{methodKey: http.MethodGet, shortMethodKey: "custom"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attributes := exportLog(t, tc.body, tc.existing)
			if !maps.Equal(attributes, tc.want) {
				t.Fatalf("attributes = %v, want %v", attributes, tc.want)
			}
		})
	}
	t.Run("the value keeps its type", func(t *testing.T) {
		logs := plog.NewLogs()
		record := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		record.Attributes().PutInt("http.response.status_code", 404)
		normalizeLogs(logs)
		status, ok := record.Attributes().Get("status_code")
		if !ok || status.Type() != pcommon.ValueTypeInt || status.Int() != 404 {
			t.Fatalf("status_code = %v, want the integer 404", status.AsRaw())
		}
	})
}

// exportLog sends one log record through the HTTP export path and returns its attributes as stored.
func exportLog(t *testing.T, body any, existing map[string]string) map[string]string {
	t.Helper()
	service, recorder, cp := newTestService(t, nil)
	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr(workspaceAttribute, testWorkspaceID)
	record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
	if err := record.Body().FromRaw(body); err != nil {
		t.Fatal(err)
	}
	for key, value := range existing {
		record.Attributes().PutStr(key, value)
	}
	request, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	if err != nil {
		t.Fatal(err)
	}
	if response := post(t, service, cp.bearer(), protobufContentType, "", request); response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", response.Code, response.Body.String())
	}
	stored := recorder.received[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	if !reflect.DeepEqual(stored.Body().AsRaw(), record.Body().AsRaw()) {
		t.Fatalf("body = %v, want %v unchanged", stored.Body().AsRaw(), record.Body().AsRaw())
	}
	attributes := map[string]string{}
	for key, value := range stored.Attributes().All() {
		attributes[key] = value.Str()
	}
	return attributes
}
