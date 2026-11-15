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
