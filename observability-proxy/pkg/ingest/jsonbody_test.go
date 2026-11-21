package ingest

import (
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
)

func TestExportedLogsCarryTheirJSONBodyFieldsAsAttributes(t *testing.T) {
	manyFields := make([]string, 0, maxBodyFields+1)
	for i := range maxBodyFields + 1 {
		manyFields = append(manyFields, fmt.Sprintf(`"f%03d":%d`, i, i))
	}
	for _, tc := range []struct {
		name     string
		body     string
		existing map[string]string
		want     map[string]string
	}{
		{
			name: "nested object, array, number, bool and null",
			body: ` {"msg":"paid","user":{"id":"42","plan":"pro"},"roles":["admin","dev"],"amount":12.50,"ok":true,"x":null}`,
			want: map[string]string{
				"msg": "paid", "user.id": "42", "user.plan": "pro", "roles.0": "admin", "roles.1": "dev",
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
		{name: "multi-line object", body: "{\"a\":1,\n\"b\":2}", want: map[string]string{}},
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
		{name: "no HTTP attributes", body: "plain text", want: map[string]string{}},
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
func exportLog(t *testing.T, body string, existing map[string]string) map[string]string {
	t.Helper()
	service, recorder, cp := newTestService(t, nil)
	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr(workspaceAttribute, testWorkspaceID)
	record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
	record.Body().SetStr(body)
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
	if stored.Body().Str() != body {
		t.Fatalf("body = %q, want it unchanged", stored.Body().Str())
	}
	attributes := map[string]string{}
	for key, value := range stored.Attributes().All() {
		attributes[key] = value.Str()
	}
	return attributes
}
