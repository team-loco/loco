package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/agent/pkg/appwatch"
	"github.com/team-loco/loco/agent/pkg/buildwatch"
	"github.com/team-loco/loco/agent/pkg/cluster"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	locov1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

type backloggedSyncServer struct {
	agentv1connect.UnimplementedAgentServiceHandler
	received chan int
}

func (s *backloggedSyncServer) Sync(ctx context.Context, stream agentv1connect.AgentServiceSyncServerStream) error {
	// Fill the response window before reading inventory, as the API's sendPending can do.
	payload := make([]byte, 1<<20)
	//nolint:gosec // Deterministic incompressible transport fixture, not a credential.
	random := rand.New(rand.NewPCG(1, 2))
	for i := range payload {
		payload[i] = byte(random.Uint32())
	}
	pending := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_Apply{Apply: &agentv1.Apply{
		PlacementId: "00000000-0000-0000-0000-000000000001", Revision: 1, Application: payload,
	}}}
	for range 8 {
		if err := stream.Send(pending); err != nil {
			return err
		}
	}
	count := 0
	for {
		msg, err := stream.Receive()
		if err != nil {
			return err
		}
		if chunk := msg.GetInventoryChunk(); chunk != nil {
			count += len(chunk.GetEntries())
			if chunk.GetComplete() {
				s.received <- count
				<-ctx.Done()
				return ctx.Err()
			}
		}
	}
}
func TestSyncReceivesPendingWorkWhileSendingItsInitialInventory(t *testing.T) {
	const placements = 100000
	ctx, cancel := context.WithCancel(t.Context())
	watcher := bootstrapWatcher(t, ctx, placements)
	handler := &backloggedSyncServer{received: make(chan int, 1)}
	server := connect.NewServer()
	agentv1connect.RegisterAgentServiceHandler(server, handler)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	cfg := testConfig()
	cfg.InventoryInterval = time.Hour
	cfg.ReconcileRetryBaseDelay = time.Second
	cfg.ReconcileRetryMaxDelay = time.Second
	cfg.SyncOutboundBuffer = 4
	cfg.BuildQueueSize = 4
	// This test exercises transport startup. Zero reconcile workers prevent application writes.
	a := &Agent{cfg: cfg, client: agentv1connect.NewAgentServiceClient(connect.NewClient(transport)),
		watcher: watcher, builds: buildwatch.Disabled{},
		inspector: cluster.NewInspector(fake.NewClientset(), "loco-system", "loco-controller"),
		domains:   testDomains(t), heartbeatInterval: time.Hour}
	done := make(chan error, 1)
	go func() { done <- a.syncLoop(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Error("sync ended without cancellation error")
			}
		case <-time.After(5 * time.Second):
			t.Error("sync workers did not stop")
		}
	})
	select {
	case got := <-handler.received:
		if got != placements {
			t.Fatalf("received %d placements, want %d", got, placements)
		}
		t.Logf("received all %d placements while the server sent 8 MiB of pending work", got)
	case err := <-done:
		t.Fatalf("sync ended before completing inventory: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("initial inventory blocked behind pending response traffic")
	}
}

// bootstrapWatcher uses the real watcher with synthetic Application list/watch responses.
func bootstrapWatcher(t *testing.T, ctx context.Context, count int) *appwatch.Watcher {
	t.Helper()
	version := locov1.GroupVersion.String()
	apps := &locov1.ApplicationList{
		APIVersion: version, Kind: "ApplicationList", ResourceVersion: "1",
	}
	for i := range count {
		apps.Items = append(apps.Items, locov1.Application{
			Status: locov1.ApplicationStatus{ObservedDeploymentID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i)},
			Name:   fmt.Sprintf("app-%d", i), Namespace: testAgentNamespace, Annotations: map[string]string{
				locov1.KeyPlacementID: fmt.Sprintf(
					"00000000-0000-0000-0000-%012d",
					i,
				),
				locov1.KeyPlacementRevision: "1",
			},
		})
	}
	gv := metav1.GroupVersionForDiscovery{GroupVersion: version, Version: locov1.GroupVersion.Version}
	groups := &metav1.APIGroupList{
		Groups: []metav1.APIGroup{
			{Name: locov1.GroupVersion.Group, Versions: []metav1.GroupVersionForDiscovery{gv}, PreferredVersion: gv},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var response any
		switch r.URL.Path {
		case "/api":
			response = &metav1.APIVersions{Versions: []string{"v1"}}
		case "/apis":
			response = groups
		case "/api/v1":
			response = &metav1.APIResourceList{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: []string{"list"}},
				},
			}
		case "/apis/" + version:
			response = &metav1.APIResourceList{
				GroupVersion: version,
				APIResources: []metav1.APIResource{
					{Name: "applications", Kind: "Application", Namespaced: true, Verbs: []string{"list", "watch"}},
				},
			}
		case "/api/v1/namespaces/" + testAgentNamespace + "/secrets":
			response = &corev1.SecretList{APIVersion: "v1", Kind: "SecretList"}
		case "/apis/" + version + "/namespaces/" + testAgentNamespace + "/applications":
			switch {
			case r.URL.Query().Get("sendInitialEvents") == "true":
				w.WriteHeader(http.StatusBadRequest)
				response = &metav1.Status{
					Status: "Failure",
					Code:   http.StatusBadRequest,
					Reason: metav1.StatusReasonBadRequest,
				}
			case r.URL.Query().Get("watch") == "true":
				w.WriteHeader(http.StatusOK)
				if err := http.NewResponseController(w).Flush(); err != nil {
					t.Error(err)
				}
				<-r.Context().Done()
				return
			default:
				response = apps
			}
		default:
			t.Errorf("unexpected Kubernetes request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	watchCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	watcher, err := appwatch.Start(watchCtx, &rest.Config{Host: srv.URL}, testAgentNamespace)
	if err != nil {
		t.Fatal(err)
	}
	return watcher
}
