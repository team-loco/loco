package identity

import (
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/team-loco/loco/internal/clusterassertion"
)

const (
	testSecret   = "identity-test"
	testLifetime = time.Minute
)

func TestEnsurePersistsTheKeyAndClusterID(t *testing.T) {
	client := fake.NewClientset()
	ctx := t.Context()
	first, err := NewStore(client, metav1.NamespaceDefault, testSecret).Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.ClusterID != "" {
		t.Fatalf("new identity has cluster ID %q", first.ClusterID)
	}
	restarted, err := NewStore(client, metav1.NamespaceDefault, testSecret).Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.Key.Equal(first.Key) {
		t.Fatal("restart generated a new key")
	}
	clusterID := uuid.NewString()
	if saveErr := NewStore(client, metav1.NamespaceDefault, testSecret).SaveClusterID(ctx, clusterID); saveErr != nil {
		t.Fatal(saveErr)
	}
	enrolled, err := NewStore(client, metav1.NamespaceDefault, testSecret).Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled.ClusterID != clusterID || !enrolled.Key.Equal(first.Key) {
		t.Fatalf("enrolled identity = %q, same key %v", enrolled.ClusterID, enrolled.Key.Equal(first.Key))
	}
}

func TestEnsureRejectsACorruptKey(t *testing.T) {
	client := fake.NewClientset(&corev1.Secret{
		Name:      testSecret,
		Namespace: metav1.NamespaceDefault,
		Data:      map[string][]byte{privateKeyField: []byte("short")},
	})
	if _, err := NewStore(client, metav1.NamespaceDefault, testSecret).Ensure(t.Context()); err == nil {
		t.Fatal("corrupt key accepted")
	}
}

func TestAssertionVerifiesWithThePublicKey(t *testing.T) {
	identity, err := NewStore(fake.NewClientset(), metav1.NamespaceDefault, testSecret).Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	identity.ClusterID = uuid.NewString()
	now := time.Now()
	assertion, err := identity.Assertion(now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := clusterassertion.Verify(assertion, identity.PublicKey(), now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ClusterID != identity.ClusterID {
		t.Fatalf("assertion issuer %q", claims.ClusterID)
	}
}
