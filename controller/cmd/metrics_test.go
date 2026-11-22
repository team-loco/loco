package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const metricsWait = 10 * time.Second

func TestTheMetricsEndpointServesReconcileQueueAndClientMetrics(t *testing.T) {
	server := httptest.NewServer(fakeAPIServer(t, nil, nil))
	defer server.Close()
	addr := freeAddress(t)
	mgr, err := ctrl.NewManager(&rest.Config{Host: server.URL}, ctrl.Options{
		Scheme:  clientgoscheme.Scheme,
		Metrics: serveMetrics(addr),
		Cache:   cache.Options{DefaultNamespaces: map[string]cache.Config{testNamespace: {}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
			return reconcile.Result{}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	want := []string{
		"controller_runtime_reconcile_total",
		"workqueue_queue_duration_seconds",
		"rest_client_rate_limiter_duration_seconds",
	}
	deadline := time.Now().Add(metricsWait)
	var body string
	for time.Now().Before(deadline) {
		body = scrape(t, "http://"+addr+"/metrics")
		if containsAll(body, want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("metrics lack one of %v:\n%s", want, body)
}

func freeAddress(t *testing.T) string {
	t.Helper()
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func scrape(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func containsAll(body string, names []string) bool {
	for _, name := range names {
		if !strings.Contains(body, name) {
			return false
		}
	}
	return true
}
