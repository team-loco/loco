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

// normalizeLogs copies the fields of each JSON object body into the record's
// attributes, keyed by dotted path (user.id) with array elements by index (roles.0), then copies
// the HTTP method and status code to method and status_code. Attributes the record already has are
// kept, and the body is left as it is. When two fields flatten to the same path, such as "a.b" and
// {"a":{"b":...}}, the first in sorted key order wins.
func normalizeLogs(logs plog.Logs) {
	for _, resourceLogs := range logs.ResourceLogs().All() {
		for _, scopeLogs := range resourceLogs.ScopeLogs().All() {
			for _, record := range scopeLogs.LogRecords().All() {
				flattenJSONBody(record)
				attributes := record.Attributes()
				aliasAttribute(attributes, "method", "http.request.method", "http.method")
				aliasAttribute(attributes, "status_code", "http.response.status_code", "http.status_code")
			}
		}
	}
}

// aliasAttribute copies the first of sources the record has to alias, unless alias is already set.
func aliasAttribute(attributes pcommon.Map, alias string, sources ...string) {
	if _, exists := attributes.Get(alias); exists {
		return
	}
	for _, source := range sources {
		if value, ok := attributes.Get(source); ok {
			value.CopyTo(attributes.PutEmpty(alias))
			return
		}
	}
}

func flattenJSONBody(record plog.LogRecord) {
	// AsString preserves text and serializes structured OTLP bodies as JSON, just as
	// the ClickHouse exporter does. Decode both forms through the same path.
	body := strings.TrimSpace(record.Body().AsString())
	if !strings.HasPrefix(body, "{") {
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
		for _, key := range slices.Sorted(maps.Keys(value)) {
			flattenJSONValue(child(key), value[key], fields)
		}
	case []any:
		for index, nested := range value {
			flattenJSONValue(child(strconv.Itoa(index)), nested, fields)
		}
	case string:
		setField(fields, path, value)
	case json.Number:
		setField(fields, path, value.String())
	case bool:
		setField(fields, path, strconv.FormatBool(value))
	case nil:
		setField(fields, path, "null")
	}
}

func setField(fields map[string]string, path, value string) {
	if _, taken := fields[path]; !taken {
		fields[path] = value
	}
}
