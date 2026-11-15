package ingest

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	// maxBodyFields bounds the attributes one log body adds.
	maxBodyFields = 100
	// maxBodyFieldKey bounds the length of a flattened attribute key.
	maxBodyFieldKey = 256
)

// flattenJSONBodies copies the fields of each single-line JSON object body into the record's
// attributes, keyed by dotted path (user.id) with array elements by index (roles.0). Attributes
// the record already has are kept, and the body is left as it is.
func flattenJSONBodies(logs plog.Logs) {
	for _, resourceLogs := range logs.ResourceLogs().All() {
		for _, scopeLogs := range resourceLogs.ScopeLogs().All() {
			for _, record := range scopeLogs.LogRecords().All() {
				flattenJSONBody(record)
			}
		}
	}
}

func flattenJSONBody(record plog.LogRecord) {
	if record.Body().Type() != pcommon.ValueTypeStr {
		return
	}
	body := strings.TrimSpace(record.Body().Str())
	if !strings.HasPrefix(body, "{") || strings.ContainsAny(body, "\r\n") {
		return
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var object map[string]any
	if decoder.Decode(&object) != nil || decoder.More() {
		return
	}
	fields := map[string]string{}
	flattenJSONValue("", object, fields)
	attributes := record.Attributes()
	added := 0
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if added == maxBodyFields {
			return
		}
		if len(key) > maxBodyFieldKey {
			continue
		}
		if _, exists := attributes.Get(key); exists {
			continue
		}
		attributes.PutStr(key, fields[key])
		added++
	}
}

func flattenJSONValue(path string, value any, fields map[string]string) {
	child := func(key string) string {
		if path == "" {
			return key
		}
		return path + "." + key
	}
	switch value := value.(type) {
	case map[string]any:
		for key, nested := range value {
			flattenJSONValue(child(key), nested, fields)
		}
	case []any:
		for index, nested := range value {
			flattenJSONValue(child(strconv.Itoa(index)), nested, fields)
		}
	case string:
		fields[path] = value
	case json.Number:
		fields[path] = value.String()
	case bool:
		fields[path] = strconv.FormatBool(value)
	case nil:
		fields[path] = "null"
	}
}
