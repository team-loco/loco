package main

import (
	"strings"
	"testing"

	registryname "github.com/google/go-containerregistry/pkg/name"
)

func TestFixtureArtifactHostUsesLoaderSupportedHTTP(t *testing.T) {
	ref, err := registryname.NewDigest(registryHost + "/platform/chart@sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme() != "http" {
		t.Fatal("fixture registry requires a transport the production loader does not infer")
	}
}
