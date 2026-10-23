package identity

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/team-loco/loco/internal/clusterassertion"
)

const (
	testNamespace = "loco-system"
	testSecret    = "loco-core-agent-key" //nolint:gosec
	testClusterID = "00000000-0000-7000-8000-000000000001"
	testLifetime  = time.Minute
)

func TestEnsurePersistsTheKeyAndClusterID(t *testing.T) {
	client := fake.NewClientset()
	ctx := t.Context()
	first, err := NewStore(client, testNamespace, testSecret).Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.ClusterID != "" {
		t.Fatalf("new identity has cluster ID %q", first.ClusterID)
	}
	restarted, err := NewStore(client, testNamespace, testSecret).Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.Key.Equal(first.Key) {
		t.Fatal("restart generated a new key")
	}
	if saveErr := NewStore(client, testNamespace, testSecret).SaveClusterID(ctx, testClusterID); saveErr != nil {
		t.Fatal(saveErr)
	}
	enrolled, err := NewStore(client, testNamespace, testSecret).Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled.ClusterID != testClusterID || !enrolled.Key.Equal(first.Key) {
		t.Fatalf("enrolled identity = %q, same key %v", enrolled.ClusterID, enrolled.Key.Equal(first.Key))
	}
}

func TestEnsureRejectsACorruptKey(t *testing.T) {
	client := fake.NewClientset(&corev1.Secret{
		Name:      testSecret,
		Namespace: testNamespace,
		Data:      map[string][]byte{privateKeyField: []byte("short")},
	})
	if _, err := NewStore(client, testNamespace, testSecret).Ensure(t.Context()); err == nil {
		t.Fatal("corrupt key accepted")
	}
}

func TestAssertionVerifiesWithThePublicKey(t *testing.T) {
	identity, err := NewStore(fake.NewClientset(), testNamespace, testSecret).Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	identity.ClusterID = testClusterID
	now := time.Now()
	assertion, err := identity.Assertion(now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := clusterassertion.Verify(assertion, identity.PublicKey(), now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ClusterID != testClusterID {
		t.Fatalf("assertion issuer %q", claims.ClusterID)
	}
}
