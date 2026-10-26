package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/team-loco/loco/internal/clusterassertion"
)

const (
	FieldOwner      = "loco-agent"
	privateKeyField = "private-key"
	clusterIDField  = "cluster-id"
)

var errInvalidKey = errors.New("agent key Secret holds no valid Ed25519 private key")

type Identity struct {
	Key       ed25519.PrivateKey
	ClusterID string
}

func (i Identity) PublicKey() ed25519.PublicKey {
	public, ok := i.Key.Public().(ed25519.PublicKey)
	if !ok {
		panic(errInvalidKey)
	}
	return public
}

func (i Identity) Assertion(now time.Time, lifetime time.Duration) (string, error) {
	return clusterassertion.Sign(i.Key, i.ClusterID, now, lifetime)
}

type Store struct {
	secrets corev1client.SecretInterface
	name    string
}

func NewStore(client kubernetes.Interface, namespace, name string) *Store {
	return &Store{secrets: client.CoreV1().Secrets(namespace), name: name}
}

func (s *Store) Ensure(ctx context.Context) (Identity, error) {
	secret, err := s.secrets.Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return s.create(ctx)
	}
	if err != nil {
		return Identity{}, fmt.Errorf("read agent key Secret %s: %w", s.name, err)
	}
	key := secret.Data[privateKeyField]
	if len(key) != ed25519.PrivateKeySize {
		return Identity{}, fmt.Errorf("%s: %w", s.name, errInvalidKey)
	}
	return Identity{Key: ed25519.PrivateKey(key), ClusterID: string(secret.Data[clusterIDField])}, nil
}

func (s *Store) create(ctx context.Context) (Identity, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate agent key: %w", err)
	}
	secret := &corev1.Secret{
		Name: s.name,
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{privateKeyField: key},
	}
	if _, err := s.secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return Identity{}, fmt.Errorf("create agent key Secret %s: %w", s.name, err)
	}
	return Identity{Key: key}, nil
}

func (s *Store) SaveClusterID(ctx context.Context, clusterID string) error {
	secret, err := s.secrets.Get(ctx, s.name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read agent key Secret %s: %w", s.name, err)
	}
	secret.Data[clusterIDField] = []byte(clusterID)
	if _, err := s.secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("record cluster ID in %s: %w", s.name, err)
	}
	return nil
}
