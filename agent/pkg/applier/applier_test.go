package applier

import (
	"context"
	"encoding/json"
	"testing"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestApplier(t *testing.T) *Applier {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	return &Applier{client: c, namespace: "loco-system"}
}

func TestApplyAndDeleteUseAgentNamespace(t *testing.T) {
	ctx := context.Background()
	a := newTestApplier(t)

	payload := DeployPayload{
		ResourceID: "abc",
		AppSpec:    &locoControllerV1.ApplicationSpec{},
	}
	specJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	err = a.ApplyFromJSON(ctx, specJSON)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	key := client.ObjectKey{Namespace: "loco-system", Name: "resource-abc"}
	got := &locoControllerV1.Application{}
	err = a.client.Get(ctx, key, got)
	if err != nil {
		t.Fatalf("get applied Application: %v", err)
	}

	err = a.ApplyFromJSON(ctx, specJSON)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}

	err = a.DeleteFromJSON(ctx, "abc")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	err = a.client.Get(ctx, key, &locoControllerV1.Application{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("Application still present after delete, err = %v", err)
	}
}
