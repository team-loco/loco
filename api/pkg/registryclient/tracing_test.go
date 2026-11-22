package registryclient

import (
	"errors"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/team-loco/loco/api/pkg/tracingtest"
)

func TestClientNamesEachOperationSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	reg := newTestRegistry(t)
	digest := reg.push(t, testRepo, "build-1")
	client := reg.client(t)
	ctx := tracingtest.StartParent(t)
	named := attribute.String(repositoryAttr, testRepo)
	if _, err := client.Repositories(ctx, "", testCatalogPage); err != nil {
		t.Fatalf("repositories: %v", err)
	}
	tracingtest.Operation(t, recorder, listRepositoriesSpan)
	if _, err := client.Tags(ctx, testRepo); err != nil {
		t.Fatalf("tags: %v", err)
	}
	tracingtest.Operation(t, recorder, listTagsSpan, named)
	if _, err := client.TagDigest(ctx, testRepo, "build-1"); err != nil {
		t.Fatalf("tag digest: %v", err)
	}
	tracingtest.Operation(t, recorder, resolveTagSpan, named)
	if err := client.DeleteManifest(ctx, testRepo, digest); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}
	tracingtest.Operation(t, recorder, deleteManifestSpan, named)
}

func TestClientOperationSpanRecordsAFailure(t *testing.T) {
	recorder := tracingtest.Record(t)
	server := tracingtest.NewHeaderServer(t, http.StatusInternalServerError)
	client, err := New(Config{URL: server.URL, Username: testUsername, Password: testPassword, Timeout: testTimeout})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err = client.TagDigest(tracingtest.StartParent(t), testRepo, "build-1"); err == nil {
		t.Fatal("digest from a failing registry succeeded")
	}
	span := tracingtest.Operation(t, recorder, resolveTagSpan)
	if span.Status().Code != codes.Error {
		t.Fatalf("%s status = %+v, want an error", resolveTagSpan, span.Status())
	}
}

func TestClientEndsAMissingTagSpanWithoutError(t *testing.T) {
	recorder := tracingtest.Record(t)
	client := newTestRegistry(t).client(t)
	if _, err := client.TagDigest(tracingtest.StartParent(t), testRepo, "missing"); !errors.Is(err, ErrTagNotFound) {
		t.Fatalf("digest of a missing tag = %v, want ErrTagNotFound", err)
	}
	span := tracingtest.Operation(t, recorder, resolveTagSpan)
	if span.Status().Code == codes.Error {
		t.Fatalf("%s status = %+v, want no error", resolveTagSpan, span.Status())
	}
}
