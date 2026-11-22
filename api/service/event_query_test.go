package service

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func TestListEventsRejectsQueriesWithThePosition(t *testing.T) {
	t.Parallel()
	server := NewEventServer(nil, nil)
	for query, want := range map[string]string{
		"(ada":                "unclosed ( at 1",
		"ada @status:200":     "unknown field @status at 5",
		"-@type:>1":           "@type does not compare numbers at 2",
		"@actor:1..2":         "@actor does not compare numbers at 1",
		"x (@request:<=5)":    "@request does not compare numbers at 4",
		`@target:"a`:          "unclosed quote at 9",
		"@target:a*b":         "* only ends a prefix, as in /api/* at 10",
		"@actor_type:system:": "quote values that contain : at 13",
	} {
		_, err := server.ListEvents(t.Context(), &eventv1.ListEventsRequest{OrgId: uuid.NewString(), Query: query})
		if got := querySyntaxDetails(t, err); got != want {
			t.Errorf("ListEvents(%q) error = %q, want %q", query, got, want)
		}
	}
}

func TestListEventsRejectsAnInvertedRangeOnEndTime(t *testing.T) {
	t.Parallel()
	server := NewEventServer(nil, nil)
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, end := range []time.Time{start, start.Add(-time.Hour)} {
		_, err := server.ListEvents(t.Context(), &eventv1.ListEventsRequest{
			OrgId:     uuid.NewString(),
			StartTime: timestamppb.New(start),
			EndTime:   timestamppb.New(end),
		})
		connectErr, ok := errors.AsType[*connect.Error](err)
		if !ok || connectErr.Code() != connect.CodeInvalidArgument || len(connectErr.Details()) != 1 {
			t.Fatalf("end %v: err = %v, want InvalidArgument with one detail", end, err)
		}
		detail, err := connectproto.UnmarshalErrorDetail(connectErr.Details()[0])
		if err != nil {
			t.Fatal(err)
		}
		violations, ok := detail.(*errdetails.BadRequest)
		if !ok || len(violations.GetFieldViolations()) != 1 {
			t.Fatalf("end %v: detail = %v, want one field violation", end, detail)
		}
		got := violations.GetFieldViolations()[0]
		if got.GetField() != "end_time" || got.GetDescription() != "must be after start_time" {
			t.Errorf("end %v: violation = %v, want end_time: must be after start_time", end, got)
		}
	}
}
