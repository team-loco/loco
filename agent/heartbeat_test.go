package main

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/team-loco/loco/agent/pkg/cluster"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	testHeartbeatInterval = 10 * time.Millisecond
	testHeartbeatWait     = 2 * time.Second
	testHeartbeatCount    = 3
)

func TestSendHeartbeatsEnqueuesOneAtOnceAndOneEveryInterval(t *testing.T) {
	inspector := cluster.NewInspector(fake.NewClientset(), "loco-system", "loco-controller")
	a := &Agent{inspector: inspector, buildsEnabled: true, heartbeatInterval: testHeartbeatInterval}
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
