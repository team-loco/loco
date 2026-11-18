package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReleaseCommandsRejectUnapprovedExecutables(t *testing.T) {
	t.Parallel()
	tool := &tool{ctx: context.Background()}
	for _, name := range []string{"sh", "bash", "curl", "rm", "/bin/sh"} {
		_, err := tool.command(name, "argument")
		require.ErrorContains(t, err, "unsupported release command")
	}
	_, err := tool.command()
	require.ErrorContains(t, err, "missing release command")
}

func TestRegistryDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, target.URL+"/v2/example/chart/manifests/latest", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	host := strings.Replace(strings.TrimPrefix(source.URL, "http://"), "127.0.0.1", "localhost", 1)
	registry, err := newRegistry("example/chart", "push", "credential", host)
	require.NoError(t, err)
	_, err = registry.manifest(context.Background(), "example/chart", "latest", false)
	require.ErrorContains(t, err, "redirected with HTTP 307")
	require.Zero(t, calls.Load())
}
