package clickhouse

import (
	"errors"
	"math"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

func TestHistoryQueryCursorsRejectTimestampOverflow(t *testing.T) {
	minimum := time.Unix(0, math.MinInt64).UTC()
	maximum := time.Unix(0, math.MaxInt64).UTC()
	for _, timestamp := range []time.Time{
		minimum.Add(-time.Nanosecond),
		maximum.Add(time.Nanosecond),
		time.Date(2611, 5, 7, 0, 0, 0, 0, time.UTC),
	} {
		t.Run(timestamp.Format(time.RFC3339Nano), func(t *testing.T) {
			position := logCursor{timestamp: timestamp, blockNumber: 1, blockOffset: 1}
			cursor := position.encode()
			start, end := time.Now().Add(-time.Hour), time.Now()
			_, _, logsErr := buildLogsQuery(testWorkspaceID, nil, start, end, "", nil, nil,
				testPageSize, cursor, observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST, testQueryTimeout, LogRegions{})
			if !errors.Is(logsErr, ErrInvalidLogCursor) {
				t.Errorf("log cursor error=%v, want invalid cursor", logsErr)
			}
			request := traceRequest(start, end)
			request.Cursor = cursor
			_, _, tracesErr := buildTracesQuery(request, testPageSize, testQueryTimeout)
			if !errors.Is(tracesErr, ErrInvalidTraceCursor) {
				t.Errorf("trace cursor error=%v, want invalid cursor", tracesErr)
			}
		})
	}
}

func TestHistoryCursorRoundTripsExactNanoseconds(t *testing.T) {
	for _, timestamp := range []time.Time{
		time.Unix(0, math.MinInt64).UTC(),
		time.Unix(0, math.MaxInt64).UTC(),
		time.Unix(0, 0).UTC(),
		time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.UTC),
		time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.FixedZone("second-offset", 1)),
	} {
		t.Run(timestamp.Format(time.RFC3339Nano), func(t *testing.T) {
			position := logCursor{timestamp: timestamp, blockNumber: math.MaxUint64, blockOffset: math.MaxUint64}
			cursor := position.encode()
			decoded, err := decodeLogCursor(cursor)
			if err != nil {
				t.Fatal(err)
			}
			if !decoded.timestamp.Equal(timestamp) || decoded.blockNumber != position.blockNumber ||
				decoded.blockOffset != position.blockOffset {
				t.Fatalf("cursor changed position: %+v", decoded)
			}
		})
	}
}
