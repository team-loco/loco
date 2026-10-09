package applier

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	testPlacementID = "p1"
	testAppUID      = types.UID("app-uid")
	databaseURLName = "DATABASE_URL"

	writeApplication  = "apply Application"
	createSecret      = "create Secret"
	updateSecret      = "update Secret"
	deleteSecretWrite = "delete Secret"
)

type writeLog struct {
	writes []string
}

func (w *writeLog) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Apply: func(
			ctx context.Context,
			c client.WithWatch,
			obj runtime.ApplyConfiguration,
			opts ...client.ApplyOption,
		) error {
			w.writes = append(w.writes, writeApplication)
			return c.Apply(ctx, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			w.writes = append(w.writes, createSecret)
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			w.writes = append(w.writes, updateSecret)
			return c.Update(ctx, obj, opts...)
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			w.writes = append(w.writes, deleteSecretWrite)
			return c.Delete(ctx, obj, opts...)
		},
	}
}

func newRecordingApplier(t *testing.T, existing ...client.Object) (*Applier, *writeLog) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	log := &writeLog{}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existing...).
		WithInterceptorFuncs(log.funcs()).
		Build()
	return &Applier{client: c, namespace: testNamespace}, log
}

func appliedApplication(revision int64, withRef bool) *locoControllerV1.Application {
	deployment := &locoControllerV1.ServiceDeploymentSpec{Image: "ghcr.io/team-loco/app:v1", Port: 8080}
	if withRef {
		deployment.EnvSecretRef = &locoControllerV1.EnvSecretRef{
			Name:     locoControllerV1.EnvSecretName(testPlacementID),
			Revision: revision,
		}
	}
	return &locoControllerV1.Application{
		Name:      applicationName(testResourceID),
		Namespace: testNamespace,
		UID:       testAppUID,
		Annotations: map[string]string{
			locoControllerV1.AnnotationPlacementID:       testPlacementID,
			locoControllerV1.AnnotationPlacementRevision: strconv.FormatInt(revision, 10),
		},
		Spec: locoControllerV1.ApplicationSpec{
			Type:        testAppType,
			ResourceID:  testResourceID,
			ServiceSpec: &locoControllerV1.ServiceSpec{Deployment: deployment},
		},
	}
}

func stagedSecret(placementID string, revision int64, value string) *corev1.Secret {
	return &corev1.Secret{
		Name:            locoControllerV1.EnvSecretName(placementID),
		Namespace:       testNamespace,
		OwnerReferences: []metav1.OwnerReference{{Kind: applicationKind, UID: testAppUID}},
		Labels: map[string]string{
			locoControllerV1.LabelPlacementID:       placementID,
			locoControllerV1.LabelPlacementRevision: strconv.FormatInt(revision, 10),
		},
		Data: map[string][]byte{databaseURLName: []byte(value)},
	}
}

func secretPlacement(t *testing.T, revision int64, value string) Placement {
	t.Helper()
	p := placement(t, testPlacementID, revision)
	p.Application = serviceApplication(t)
	p.EnvSecret = &EnvSecret{Revision: revision, Data: map[string][]byte{databaseURLName: []byte(value)}}
	return p
}

func (a *Applier) stagedValue(t *testing.T) (string, string) {
	t.Helper()
	secret, err := a.envSecret(t)
	if err != nil {
		t.Fatalf("get env Secret: %v", err)
	}
	return string(secret.Data[databaseURLName]), secret.Labels[locoControllerV1.LabelPlacementRevision]
}

func TestFirstCreationAppliesTheApplicationBeforeItsEnvSecret(t *testing.T) {
	a, log := newRecordingApplier(t)
	if err := a.ApplyPlacement(context.Background(), secretPlacement(t, 1, "v1")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := []string{writeApplication, createSecret}; !slices.Equal(log.writes, want) {
		t.Fatalf("writes = %v, want %v", log.writes, want)
	}
	app, err := a.live(t)
	if err != nil {
		t.Fatalf("get Application: %v", err)
	}
	secret, err := a.envSecret(t)
	if err != nil {
		t.Fatalf("get env Secret: %v", err)
	}
	owners := secret.OwnerReferences
	if len(owners) != 1 || owners[0].Name != app.Name || owners[0].UID != app.UID || owners[0].Kind != applicationKind {
		t.Fatalf("env Secret owners = %+v, want the Application %s (%s)", owners, app.Name, app.UID)
	}
}

func TestAnUpdateWritesTheEnvSecretBeforeTheApplication(t *testing.T) {
	a, log := newRecordingApplier(t, appliedApplication(2, true), stagedSecret(testPlacementID, 2, "v2"))
	if err := a.ApplyPlacement(context.Background(), secretPlacement(t, 4, "v4")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := []string{updateSecret, writeApplication}; !slices.Equal(log.writes, want) {
		t.Fatalf("writes = %v, want %v", log.writes, want)
	}
	if value, revision := a.stagedValue(t); value != "v4" || revision != "4" {
		t.Fatalf("env Secret = %q at revision %s, want v4 at 4", value, revision)
	}
	secret, err := a.envSecret(t)
	if err != nil {
		t.Fatalf("get env Secret: %v", err)
	}
	if owners := secret.OwnerReferences; len(owners) != 1 || owners[0].UID != testAppUID {
		t.Fatalf("env Secret owners = %+v, want the Application", owners)
	}
}

func TestAnEnvSecretThatIsNotNewerIsRejectedBeforeAnyWrite(t *testing.T) {
	cases := []struct {
		name     string
		existing []client.Object
		apply    int64
	}{
		{
			name:     "older than the applied Application",
			existing: []client.Object{appliedApplication(5, true), stagedSecret(testPlacementID, 5, "v5")},
			apply:    4,
		},
		{
			name:     "older than the staged Secret",
			existing: []client.Object{appliedApplication(5, true), stagedSecret(testPlacementID, 6, "v6")},
			apply:    5,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, log := newRecordingApplier(t, tc.existing...)
			err := a.ApplyPlacement(context.Background(), secretPlacement(t, tc.apply, "stale"))
			if !errors.Is(err, ErrStaleRevision) {
				t.Fatalf("apply = %v, want ErrStaleRevision", err)
			}
			if len(log.writes) != 0 {
				t.Fatalf("a stale Apply wrote %v", log.writes)
			}
		})
	}
}

func TestARetryAtTheStagedRevisionSkipsTheSecretWrite(t *testing.T) {
	a, log := newRecordingApplier(t, appliedApplication(2, true), stagedSecret(testPlacementID, 3, "v3"))
	if err := a.ApplyPlacement(context.Background(), secretPlacement(t, 3, "v3")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := []string{writeApplication}; !slices.Equal(log.writes, want) {
		t.Fatalf("writes = %v, want %v", log.writes, want)
	}
	app, err := a.live(t)
	if err != nil {
		t.Fatalf("get Application: %v", err)
	}
	if ref := app.Spec.ServiceSpec.Deployment.EnvSecretRef; ref == nil || ref.Revision != 3 {
		t.Fatalf("envSecretRef = %+v, want revision 3", ref)
	}
}

func TestAnApplicationWithoutARefGetsItBeforeTheSecretIsWritten(t *testing.T) {
	a, log := newRecordingApplier(t, appliedApplication(2, false), stagedSecret(testPlacementID, 1, "old"))
	if err := a.ApplyPlacement(context.Background(), secretPlacement(t, 3, "v3")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := []string{writeApplication, updateSecret}; !slices.Equal(log.writes, want) {
		t.Fatalf("writes = %v, want %v", log.writes, want)
	}
}

func TestAnEnvSecretAtAnotherRevisionThanTheApplyIsInvalid(t *testing.T) {
	a, log := newRecordingApplier(t)
	p := secretPlacement(t, 3, "v3")
	p.EnvSecret.Revision = 2
	if err := a.ApplyPlacement(context.Background(), p); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("apply = %v, want ErrInvalidPayload", err)
	}
	if len(log.writes) != 0 {
		t.Fatalf("an invalid Apply wrote %v", log.writes)
	}
}

func TestAPlacementWithoutSecretsDeletesItsStagedSecretAfterTheApplication(t *testing.T) {
	a, log := newRecordingApplier(t, appliedApplication(2, true), stagedSecret(testPlacementID, 2, "v2"))
	p := placement(t, testPlacementID, 3)
	p.Application = serviceApplication(t)
	if err := a.ApplyPlacement(context.Background(), p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := []string{writeApplication, deleteSecretWrite}; !slices.Equal(log.writes, want) {
		t.Fatalf("writes = %v, want %v", log.writes, want)
	}
	if _, err := a.envSecret(t); !apierrors.IsNotFound(err) {
		t.Fatalf("env Secret: err = %v, want not found", err)
	}
}

func TestSweepEnvSecretsDeletesOrphans(t *testing.T) {
	referenced := appliedApplication(2, true)
	unreferencing := appliedApplication(2, false)
	unreferencing.Name = applicationName("other")
	unreferencing.Annotations[locoControllerV1.AnnotationPlacementID] = "p2"
	unrelated := &corev1.Secret{Name: "registry", Namespace: testNamespace}
	a, _ := newRecordingApplier(t,
		referenced,
		unreferencing,
		unrelated,
		stagedSecret(testPlacementID, 2, "kept"),
		stagedSecret("p2", 2, "unreferenced"),
		stagedSecret("p3", 2, "no placement"),
	)

	if err := a.SweepEnvSecrets(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	secrets := &corev1.SecretList{}
	if err := a.client.List(context.Background(), secrets, client.InNamespace(testNamespace)); err != nil {
		t.Fatalf("list: %v", err)
	}
	names := make([]string, 0, len(secrets.Items))
	for _, secret := range secrets.Items {
		names = append(names, secret.Name)
	}
	slices.Sort(names)
	if want := []string{"env-p1", "registry"}; !slices.Equal(names, want) {
		t.Fatalf("secrets after the sweep = %v, want %v", names, want)
	}
}

func TestARetryRewritesASecretThatAnotherApplicationOwned(t *testing.T) {
	orphan := stagedSecret(testPlacementID, 3, "v3")
	orphan.OwnerReferences[0].UID = "deleted-app-uid"
	a, log := newRecordingApplier(t, appliedApplication(2, true), orphan)
	if err := a.ApplyPlacement(context.Background(), secretPlacement(t, 3, "v3")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := []string{updateSecret, writeApplication}; !slices.Equal(log.writes, want) {
		t.Fatalf("writes = %v, want %v", log.writes, want)
	}
	secret, err := a.envSecret(t)
	if err != nil {
		t.Fatalf("get env Secret: %v", err)
	}
	if owners := secret.OwnerReferences; len(owners) != 1 || owners[0].UID != testAppUID {
		t.Fatalf("env Secret owners = %+v, want the live Application", owners)
	}
}
