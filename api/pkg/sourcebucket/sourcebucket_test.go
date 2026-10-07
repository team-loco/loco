package sourcebucket

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPresignPutUsesVirtualHostedStyle(t *testing.T) {
	bucket, err := New(Config{
		Endpoint:        "https://storage.example.com",
		Bucket:          "loco-sources",
		Region:          "auto",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
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

func TestNewRejectsPartialConfig(t *testing.T) {
	if _, err := New(Config{Bucket: "loco-sources"}); err == nil {
		t.Fatal("a bucket without credentials was accepted")
	}
}
