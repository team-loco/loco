package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/agent/pkg/applier"
	"github.com/team-loco/loco/agent/pkg/buildwatch"
	"github.com/team-loco/loco/agent/pkg/cluster"
	"github.com/team-loco/loco/agent/pkg/kube"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// The API database regression starts this fixture in a child process to exercise both real
// modules without making the agent depend on the API module. Kubernetes objects are synthetic.
func TestSyncAgainstRealAPIBacklog(t *testing.T) {
	endpoint := os.Getenv("LOCO_TEST_SYNC_URL")
	if endpoint == "" {
		t.Skip("started by the API duplex database regression")
	}
	slog.SetDefault(slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+os.Getenv("LOCO_TEST_SYNC_AUTH"))
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(os.Getenv("LOCO_TEST_SYNC_CERTIFICATE"))) {
		t.Fatal("invalid fixture certificate")
	}
	httpClient := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		// A small valid window exercises sustained backpressure with 500 builds instead of tens of thousands.
		HTTP2: &http.HTTP2Config{MaxReceiveBufferPerStream: 16 << 10},
	}}
	watcher := bootstrapWatcher(t, ctx, 1000)
	scheme, err := kube.NewScheme()
	if err != nil {
		t.Fatal(err)
	}
	inventorySeen := make(chan struct{}, 1)
	clusterClient := fakeclient.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*corev1.NamespaceList); ok {
				select {
				case inventorySeen <- struct{}{}:
				default:
				}
			}
			return c.List(ctx, list, opts...)
		},
	}).Build()
	cfg := testConfig()
	cfg.Role = clusterv1.ClusterRole_CLUSTER_ROLE_ALL
	cfg.InventoryInterval = time.Hour
	cfg.ReconcileWorkers = 1
	cfg.ReconcileRetryBaseDelay = time.Second
	cfg.ReconcileRetryMaxDelay = time.Second
	cfg.SyncOutboundBuffer = 256
	cfg.BuildQueueSize = 64
	a := &Agent{
		cfg: cfg,
		client: agentv1connect.NewAgentServiceClient(
			connect.NewClient(connecthttp.NewTransport(httpClient, endpoint)),
		),
		watcher: watcher,
		builds:  buildwatch.Disabled{},
		applier: applier.NewWithClient(clusterClient, testAgentNamespace, nil),
		inspector: cluster.NewInspector(
			fake.NewClientset(),
			"loco-system",
			"loco-controller",
		),
		domains:           testDomains(t),
		heartbeatInterval: time.Hour,
	}
	done := make(chan struct{})
	var syncErr error
	go func() {
		syncErr = a.syncLoop(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
			if syncErr == nil {
				t.Error("sync ended without cancellation error")
			}
		case <-time.After(5 * time.Second):
			t.Error("sync did not stop")
		}
	}()
	snapshot := false
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-inventorySeen:
			snapshot = true
		case <-ticker.C:
			//nolint:gosec // The parent database test supplies this local TLS fixture URL and its certificate.
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/test/build-failures", nil)
			if err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // This request targets only the parent test's local TLS fixture.
			response, err := httpClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode == http.StatusOK && snapshot {
				t.Log(
					"received complete inventory reply and API stored every permanent build failure with default agent buffers",
				)
				return
			}
		case <-done:
			t.Fatalf("sync stopped before inventory and build reports completed: %v", syncErr)
		case <-ctx.Done():
			t.Fatal("inventory or build reports stalled under response backpressure")
		}
	}
}
