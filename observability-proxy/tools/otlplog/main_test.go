package main

import (
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
)

const (
	testWorkspaceID = "11111111-2222-3333-4444-555555555555"
	testBody        = "control-plane-ingest-0123abcd"
)

func TestExportRequestCarriesTheWorkspaceBodyAndTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	payload, err := exportRequest(testWorkspaceID, testBody, now)
	if err != nil {
		t.Fatal(err)
	}
	request := plogotlp.NewExportRequest()
	if err := request.UnmarshalProto(payload); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	resourceLogs := request.Logs().ResourceLogs().At(0)
	workspace, ok := resourceLogs.Resource().Attributes().Get(workspaceAttribute)
	if !ok || workspace.Str() != testWorkspaceID {
		t.Fatalf("workspace attribute = %v, want %q", workspace.AsRaw(), testWorkspaceID)
	}
	record := resourceLogs.ScopeLogs().At(0).LogRecords().At(0)
	if got := record.Body().Str(); got != testBody {
		t.Fatalf("body = %q, want %q", got, testBody)
	}
	if got := record.Timestamp().AsTime(); !got.Equal(now) {
		t.Fatalf("timestamp = %s, want %s", got, now)
	}
}

func TestExportRequestRequiresWorkspaceAndBody(t *testing.T) {
	if _, err := exportRequest("", testBody, time.Now()); !errors.Is(err, errMissingFlags) {
		t.Fatalf("error = %v, want %v", err, errMissingFlags)
	}
}

func TestRejectedRecordsReadsPartialSuccess(t *testing.T) {
	for _, want := range []int64{0, 1} {
		response := plogotlp.NewExportResponse()
		if want > 0 {
			response.PartialSuccess().SetRejectedLogRecords(want)
		}
		payload, err := response.MarshalProto()
		if err != nil {
			t.Fatal(err)
		}
		got, err := rejectedRecords(payload)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("rejected = %d, want %d", got, want)
		}
	}
}
