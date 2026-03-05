package timeutil

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// ParsePostgresTimestamp converts a time.Time to a protobuf Timestamp.
func ParsePostgresTimestamp(ts time.Time) *timestamppb.Timestamp {
	return timestamppb.New(ts)
}

// ParsePostgresTimestampPtr converts a *time.Time to a protobuf Timestamp.
func ParsePostgresTimestampPtr(ts *time.Time) *timestamppb.Timestamp {
	if ts == nil {
		return nil
	}
	return ParsePostgresTimestamp(*ts)
}
