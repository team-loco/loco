package cluster

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKeyUsesSystemNamespaceUID(t *testing.T) {
	const expected = "00000000-0000-7000-8000-000000000001"
	client := fake.NewClientset(&corev1.Namespace{Name: metav1.NamespaceSystem, UID: types.UID(expected)})
	for range 2 {
		inspector := NewInspector(client, "loco-system", "controller")
		key, err := inspector.Key(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if key != expected {
			t.Fatalf("key %q, want %q", key, expected)
		}
	}
	if err := client.CoreV1().
		Namespaces().
		Delete(context.Background(), metav1.NamespaceSystem, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewInspector(client, "", "").Key(context.Background()); err == nil {
		t.Fatal("missing system namespace accepted")
	}
}
