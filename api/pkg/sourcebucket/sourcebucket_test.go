package sourcebucket

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/tracingtest"
)

const (
	testBucket      = "loco-sources"
	testAccessKeyID = "key"
	testSecretKey   = "secret"
)

func TestPresignPutUsesVirtualHostedStyle(t *testing.T) {
	bucket, err := New(Config{
		Endpoint:        "https://storage.example.com",
		Bucket:          testBucket,
		Region:          "auto",
		AccessKeyID:     testAccessKeyID,
		SecretAccessKey: testSecretKey,
	})
	if err != nil {
		t.Fatalf("new bucket: %v", err)
	}

	ctx := context.Background()
	raw, err := bucket.PresignPut(ctx, "sources/abc.tar.gz", 1234, 15*time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	presigned, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse presigned url: %v", err)
	}

	if presigned.Host != "loco-sources.storage.example.com" {
		t.Fatalf("host = %q, want the bucket as a subdomain", presigned.Host)
	}
	if presigned.Path != "/sources/abc.tar.gz" {
		t.Fatalf("path = %q, want /sources/abc.tar.gz", presigned.Path)
	}
	query := presigned.Query()
	if expires := query.Get("X-Amz-Expires"); expires != "900" {
		t.Fatalf("expires = %q, want 900", expires)
	}
	if signed := query.Get("X-Amz-SignedHeaders"); !strings.Contains(signed, "content-length") {
		t.Fatalf("signed headers = %q, want content-length signed", signed)
	}
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-amz-checksum") || lower == "x-amz-sdk-checksum-algorithm" {
			t.Fatalf("presigned url carries checksum parameter %q", key)
		}
	}
}

func TestPresignGetUsesPathStyleWhenForced(t *testing.T) {
	bucket, err := New(Config{
		Endpoint:        "http://10.0.0.5:7070",
		Bucket:          testBucket,
		Region:          "us-east-1",
		AccessKeyID:     testAccessKeyID,
		SecretAccessKey: testSecretKey,
		ForcePathStyle:  true,
	})
	if err != nil {
		t.Fatalf("new bucket: %v", err)
	}

	ctx := context.Background()
	raw, err := bucket.PresignGet(ctx, "sources/abc.tar.gz", time.Hour)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	presigned, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse presigned url: %v", err)
	}

	if presigned.Host != "10.0.0.5:7070" {
		t.Fatalf("host = %q, want the endpoint host", presigned.Host)
	}
	if presigned.Path != "/loco-sources/sources/abc.tar.gz" {
		t.Fatalf("path = %q, want the bucket in the path", presigned.Path)
	}
	query := presigned.Query()
	if expires := query.Get("X-Amz-Expires"); expires != "3600" {
		t.Fatalf("expires = %q, want 3600", expires)
	}
}

func TestNewRejectsPartialConfig(t *testing.T) {
	if _, err := New(Config{Bucket: testBucket}); err == nil {
		t.Fatal("a bucket without credentials was accepted")
	}
}

func TestBucketPropagatesTraceContext(t *testing.T) {
	tracingtest.Record(t)
	server := tracingtest.NewHeaderServer(t, http.StatusNoContent)
	bucket, err := New(Config{
		Endpoint:        server.URL,
		Bucket:          testBucket,
		Region:          "auto",
		AccessKeyID:     testAccessKeyID,
		SecretAccessKey: testSecretKey,
		ForcePathStyle:  true,
	})
	if err != nil {
		t.Fatalf("new bucket: %v", err)
	}
	ctx := tracingtest.StartParent(t)
	if err := bucket.Delete(ctx, "sources/abc.tar.gz"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	tracingtest.AssertContinues(t, server.Traceparent(), ctx)
}
