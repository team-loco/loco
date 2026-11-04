package buildwatch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testRegistryHost   = "registry.test"
	testCredential     = "first-credential" //nolint:gosec
	testNextCredential = "second-credential"
	createdSecret      = "Secret"
	createdBuild       = "Build"
)

func tokenConfig() Config {
	cfg := testConfig()
	cfg.TokenMode = true
	return cfg
}

func tokenStart(buildID, credential string) *agentv1.StartBuild {
	start := startMessage(buildID)
	start.RegistryCredential = credential
	return start
}

type registryAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type recorder struct {
	created      []string
	secretWrites int
}

func (r *recorder) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			switch obj.(type) {
			case *corev1.Secret:
				r.created = append(r.created, createdSecret)
				r.secretWrites++
			case *locoControllerV1.Build:
				r.created = append(r.created, createdBuild)
			}
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				r.secretWrites++
			}
			return c.Update(ctx, obj, opts...)
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				r.secretWrites++
			}
			return c.Delete(ctx, obj, opts...)
		},
	}
}

func newRecordingWatcher(t *testing.T, cfg Config) (*Watcher, client.Client, *recorder) {
	t.Helper()
	rec := &recorder{}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithInterceptorFuncs(rec.funcs()).Build()
	return New(c, c, cfg), c, rec
}

func registrySecretAuth(t *testing.T, c client.Client, buildID string) (registryAuth, error) {
	t.Helper()
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: testNamespace, Name: locoControllerV1.BuildRegistrySecretName(buildID)}
	if err := c.Get(context.Background(), key, secret); err != nil {
		return registryAuth{}, err
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson {
		t.Fatalf("build registry Secret type = %s, want %s", secret.Type, corev1.SecretTypeDockerConfigJson)
	}
	var config struct {
		Auths map[string]registryAuth `json:"auths"`
	}
	if err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config); err != nil {
		t.Fatalf("decode docker config: %v", err)
	}
	auth, ok := config.Auths[testRegistryHost]
	if !ok || len(config.Auths) != 1 {
		t.Fatalf("auths = %v, want only %s", config.Auths, testRegistryHost)
	}
	return auth, nil
}

func TestTokenModeWritesTheRegistrySecretBeforeTheBuild(t *testing.T) {
	w, c, rec := newRecordingWatcher(t, tokenConfig())

	if err := w.Create(context.Background(), tokenStart(testBuildID, testCredential)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(rec.created) != 2 || rec.created[0] != createdSecret || rec.created[1] != createdBuild {
		t.Fatalf("created %v, want the Secret before the Build", rec.created)
	}
	auth, err := registrySecretAuth(t, c, testBuildID)
	if err != nil {
		t.Fatalf("get build registry Secret: %v", err)
	}
	if auth.Username != locoControllerV1.RegistryBuildUsername || auth.Password != testCredential {
		t.Fatalf("credential = %q / %q, want %s / %s",
			auth.Username, auth.Password, locoControllerV1.RegistryBuildUsername, testCredential)
	}
}

func TestAReplayedStartReplacesTheRegistryCredential(t *testing.T) {
	w, c, _ := newRecordingWatcher(t, tokenConfig())
	ctx := context.Background()

	if err := w.Create(ctx, tokenStart(testBuildID, testCredential)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.Create(ctx, tokenStart(testBuildID, testNextCredential)); err != nil {
		t.Fatalf("replayed create: %v", err)
	}
	auth, err := registrySecretAuth(t, c, testBuildID)
	if err != nil {
		t.Fatalf("get build registry Secret: %v", err)
	}
	if auth.Password != testNextCredential {
		t.Fatalf("password after a replay = %q, want %q", auth.Password, testNextCredential)
	}
}

func TestTheRegistrySecretIsDeletedWithTheBuild(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	w, c, _ := newRecordingWatcher(t, tokenConfig())
	w.now = func() time.Time { return now }
	ctx := context.Background()
	for _, id := range []string{testBuildID, testOtherID} {
		if err := w.Create(ctx, tokenStart(id, testCredential)); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	if err := w.Delete(ctx, testBuildID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := registrySecretAuth(t, c, testBuildID); !apierrors.IsNotFound(err) {
		t.Fatalf("registry Secret of a deleted build: err = %v, want not found", err)
	}

	finished, err := getBuild(t, c, testOtherID)
	if err != nil {
		t.Fatalf("get build: %v", err)
	}
	old := metav1.NewTime(now.Add(-2 * time.Hour))
	finished.Status.Phase = locoControllerV1.BuildPhaseSucceeded
	finished.Status.FinishedAt = &old
	if updateErr := c.Update(ctx, finished); updateErr != nil {
		t.Fatalf("finish build: %v", updateErr)
	}
	if collectErr := w.Collect(ctx); collectErr != nil {
		t.Fatalf("collect: %v", collectErr)
	}
	if _, err := registrySecretAuth(t, c, testOtherID); !apierrors.IsNotFound(err) {
		t.Fatalf("registry Secret of a collected build: err = %v, want not found", err)
	}
}

func TestTokenModeRefusesAStartWithoutACredential(t *testing.T) {
	w, c, rec := newRecordingWatcher(t, tokenConfig())

	err := w.Create(context.Background(), tokenStart(testBuildID, ""))
	if !errors.Is(err, ErrInvalidBuild) {
		t.Fatalf("create without a credential = %v, want ErrInvalidBuild", err)
	}
	if _, getErr := getBuild(t, c, testBuildID); !apierrors.IsNotFound(getErr) {
		t.Fatalf("Build after a refused start: err = %v, want not found", getErr)
	}
	if rec.secretWrites != 0 {
		t.Fatalf("Secret writes = %d, want none", rec.secretWrites)
	}
}

func TestStaticModeWritesNoRegistrySecret(t *testing.T) {
	w, c, rec := newRecordingWatcher(t, testConfig())
	ctx := context.Background()

	if err := w.Create(ctx, tokenStart(testBuildID, testCredential)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.Delete(ctx, testBuildID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rec.secretWrites != 0 {
		t.Fatalf("Secret writes in static mode = %d, want none", rec.secretWrites)
	}
	if _, err := getBuild(t, c, testBuildID); !apierrors.IsNotFound(err) {
		t.Fatalf("Build after delete: err = %v, want not found", err)
	}
}
