package service

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestListEventsRejectsAnEmptyOrReversedTimeRange(t *testing.T) {
	server := NewEventServer(nil, nil)
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
