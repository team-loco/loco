package sourcebucket

import (
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/team-loco/loco/api/pkg/tracingtest"
)

func TestBucketNamesEachOperationSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	bucket := newTestBucket(t, tracingtest.NewHeaderServer(t, http.StatusNoContent).URL)
	ctx := tracingtest.StartParent(t)
	named := attribute.String(bucketAttr, testBucket)
	if err := bucket.Delete(ctx, testKey); err != nil {
		t.Fatalf("delete: %v", err)
	}
	tracingtest.Operation(t, recorder, deleteSpan, named)
	if _, err := bucket.ObjectSize(ctx, testKey); err != nil {
		t.Fatalf("object size: %v", err)
	}
	tracingtest.Operation(t, recorder, headSpan, named)
	if _, _, err := bucket.List(ctx, "sources/", "", 10); err != nil {
		t.Fatalf("list: %v", err)
	}
	tracingtest.Operation(t, recorder, listSpan, named)
}

func TestBucketOperationSpanRecordsAFailure(t *testing.T) {
	recorder := tracingtest.Record(t)
	bucket := newTestBucket(t, tracingtest.NewHeaderServer(t, http.StatusNotFound).URL)
	if _, err := bucket.ObjectSize(tracingtest.StartParent(t), testKey); err == nil {
		t.Fatal("object size of a missing object succeeded")
	}
	span := tracingtest.Operation(t, recorder, headSpan)
	if span.Status().Code != codes.Error {
		t.Fatalf("%s status = %+v, want an error", headSpan, span.Status())
	}
}
