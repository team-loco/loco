// Package lastactive formats the last session activity locoadm tables show.
package lastactive

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Format returns the time in UTC, or a dash when there is no recorded session activity.
func Format(at *timestamppb.Timestamp) string {
	if at == nil {
		return "—"
	}
	return at.AsTime().Format(time.DateTime)
}
