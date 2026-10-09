package resource

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestWriteEventsJSONPrintsRFC3339Timestamps(t *testing.T) {
	at := time.Date(2026, time.October, 9, 12, 30, 45, 0, time.UTC)
	events := []*resourcev1.Event{
		{Reason: "Scheduled", Message: "assigned to node", Timestamp: timestamppb.New(at)},
		{Reason: "Pulled", Message: "image pulled", Timestamp: timestamppb.New(at.Add(time.Second))},
	}

	var out bytes.Buffer
	if err := writeEventsJSON(&out, events); err != nil {
		t.Fatalf("writeEventsJSON: %v", err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out.String())
	}
	if len(decoded) != len(events) {
		t.Fatalf("%d items, want %d\n%s", len(decoded), len(events), out.String())
	}
	for i, item := range decoded {
		stamp, ok := item["timestamp"].(string)
		if !ok {
			t.Fatalf("event %d timestamp = %v, want an RFC 3339 string\n%s", i, item["timestamp"], out.String())
		}
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			t.Fatalf("event %d timestamp %q: %v", i, stamp, err)
		}
		if !parsed.Equal(events[i].GetTimestamp().AsTime()) {
			t.Fatalf("event %d timestamp = %s, want %s", i, parsed, events[i].GetTimestamp().AsTime())
		}
	}
}

func TestWriteEventsJSONPrintsAnEmptyArray(t *testing.T) {
	var out bytes.Buffer
	if err := writeEventsJSON(&out, nil); err != nil {
		t.Fatalf("writeEventsJSON: %v", err)
	}
	if got := out.String(); got != "[]\n" {
		t.Fatalf("output = %q, want an empty JSON array", got)
	}
}
