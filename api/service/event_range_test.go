package service

import (
	"testing"
	"time"

	"buf.build/go/protovalidate"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"

	"connectrpc.com/connect/v2"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestListEventsRejectsAnEmptyOrReversedTimeRange(t *testing.T) {
	server := NewEventServer(nil)
	at := time.Now()
	for _, end := range []time.Time{at, at.Add(-time.Second)} {
		_, err := server.ListEvents(
			t.Context(),
			&eventv1.ListEventsRequest{StartTime: timestamppb.New(at), EndTime: timestamppb.New(end)},
		)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("range error = %v", err)
		}
	}
}

func TestListRequestsAllowRemainingUserLimit(t *testing.T) {
	const id = "00000000-0000-7000-8000-000000000001"
	if err := protovalidate.Validate(&eventv1.ListEventsRequest{OrgId: id, PageSize: 1001}); err != nil {
		t.Fatalf("event limit: %v", err)
	}
	if err := protovalidate.Validate(&buildv1.ListBuildsRequest{ResourceId: id, PageSize: 1001}); err != nil {
		t.Fatalf("build limit: %v", err)
	}
}
