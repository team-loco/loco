package main

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/team-loco/loco/agent/pkg/applier"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testAgentNamespace = "loco-system"
	testOrphanSecret   = "orphan"
	testSweepPoll      = 10 * time.Millisecond
)

func TestSweepEnvSecretsDeletesOrphanedSecretsWithoutASyncStream(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := locov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	orphan := &corev1.Secret{
		Name:      testOrphanSecret,
		Namespace: testAgentNamespace,
		Labels:    map[string]string{locov1alpha1.LabelPlacementID: testOrphanSecret},
	}
	kube := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(orphan).Build()
	a := &Agent{
		cfg:     &Config{InventoryInterval: time.Hour},
		applier: applier.NewWithClient(kube, testAgentNamespace, nil),
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		a.sweepEnvSecrets(ctx)
	}()
	defer func() {
		cancel()
		<-stopped
	}()

	key := client.ObjectKeyFromObject(orphan)
	deadline := time.After(testHeartbeatWait)
	for {
		err := kube.Get(ctx, key, &corev1.Secret{})
		if apierrors.IsNotFound(err) {
			return
		}
		if err != nil {
			t.Fatalf("get Secret: %v", err)
		}
		select {
		case <-deadline:
			t.Fatalf("orphaned Secret %s still exists after %s", testOrphanSecret, testHeartbeatWait)
		case <-time.After(testSweepPoll):
		}
	}
}
