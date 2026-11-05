package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/team-loco/loco/agent/pkg/cluster"
	"github.com/team-loco/loco/agent/pkg/domains"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	testHeartbeatInterval = 10 * time.Millisecond
	testDetectInterval    = 10 * time.Millisecond
	testHeartbeatWait     = 2 * time.Second
	testHeartbeatCount    = 3
	testServedDomain      = "apps.test"
	testGatewayNamespace  = "platform"
	testGateway           = "ingress"
	testListener          = "https-apps-0"
	testGatewayGeneration = 1
	testLocoGroupVersion  = "infra.loco.io/v1alpha1"
	testBuildResource     = "builds"
)

func testDomains(t *testing.T) *domains.Reporter {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	host := gatewayv1.Hostname("*." + testServedDomain)
	ready := func(condition gatewayv1.ListenerConditionType) metav1.Condition {
		return metav1.Condition{
			Type:               string(condition),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: testGatewayGeneration,
		}
	}
	gateway := &gatewayv1.Gateway{
		Name:       testGateway,
		Namespace:  testGatewayNamespace,
		Generation: testGatewayGeneration,
		Spec:       gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{{Name: testListener, Hostname: &host}}},
		Status: gatewayv1.GatewayStatus{Listeners: []gatewayv1.ListenerStatus{{
			Name: testListener,
			Conditions: []metav1.Condition{
				ready(gatewayv1.ListenerConditionAccepted),
				ready(gatewayv1.ListenerConditionProgrammed),
			},
		}}},
	}
	reader := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(gateway).Build()
	return domains.New(reader, domains.Config{
		Namespace: testGatewayNamespace,
		Gateway:   testGateway,
		Domains:   []string{testServedDomain},
	})
}

func inspectorServingBuilds(t *testing.T, served bool) *cluster.Inspector {
	t.Helper()
	client := fake.NewClientset()
	discovery, ok := client.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		t.Fatal("fake clientset has no fake discovery")
	}
	if served {
		discovery.Resources = []*metav1.APIResourceList{{
			GroupVersion: testLocoGroupVersion,
			APIResources: []metav1.APIResource{{Name: testBuildResource, Kind: "Build", Namespaced: true}},
		}}
	}
	return cluster.NewInspector(client, "loco-system", "loco-controller")
}

func TestSendHeartbeatsEnqueuesOneAtOnceAndOneEveryInterval(t *testing.T) {
	inspector := cluster.NewInspector(fake.NewClientset(), "loco-system", "loco-controller")
	a := &Agent{
		inspector:         inspector,
		domains:           testDomains(t),
		buildsEnabled:     true,
		heartbeatInterval: testHeartbeatInterval,
	}
	ctx, cancel := context.WithCancel(t.Context())
	sent := make(chan *agentv1.SyncRequest)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		a.sendHeartbeats(ctx, func(msg *agentv1.SyncRequest) {
			select {
			case sent <- msg:
			case <-ctx.Done():
			}
		})
	}()

	for range testHeartbeatCount {
		select {
		case msg := <-sent:
			if !msg.GetHeartbeat().GetBuildsEnabled() {
				t.Fatalf("sent %v, want a heartbeat that reports builds enabled", msg)
			}
			if !slices.Equal(msg.GetHeartbeat().GetDomains(), []string{testServedDomain}) {
				t.Fatalf("sent %v, want a heartbeat that reports %s", msg, testServedDomain)
			}
		case <-time.After(testHeartbeatWait):
			t.Fatalf("no heartbeat within %s", testHeartbeatWait)
		}
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(testHeartbeatWait):
		t.Fatal("heartbeats kept going after the sync stream's context ended")
	}
}

func TestWatchBuildSupportStopsTheAgentWhenSupportChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), testHeartbeatWait)
	defer cancel()
	err := watchBuildSupport(ctx, inspectorServingBuilds(t, true), false, testDetectInterval)
	if !errors.Is(err, errBuildSupportChanged) {
		t.Fatalf("watchBuildSupport returned %v, want errBuildSupportChanged", err)
	}
}

func TestWatchBuildSupportKeepsRunningWhileSupportIsUnchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*testDetectInterval)
	defer cancel()
	err := watchBuildSupport(ctx, inspectorServingBuilds(t, true), true, testDetectInterval)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("watchBuildSupport returned %v, want it to run until the context ends", err)
	}
}
