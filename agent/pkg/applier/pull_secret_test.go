package applier

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const testWorkspaceID = "01a33333-3333-7333-8333-333333333333"

var errIssueRefused = errors.New("control plane unavailable")

type recordingPullSecrets struct {
	client     client.Client
	err        error
	workspaces []string
	appExisted bool
}

func (r *recordingPullSecrets) Ensure(ctx context.Context, workspaceID string) error {
	r.workspaces = append(r.workspaces, workspaceID)
	app := &locoControllerV1.Application{}
	key := client.ObjectKey{Namespace: testNamespace, Name: locoControllerV1.ApplicationName(testResourceID)}
	if err := r.client.Get(ctx, key, app); !apierrors.IsNotFound(err) {
		r.appExisted = true
	}
	if r.err != nil {
		return r.err
	}
	secret := &corev1.Secret{
		Name:      locoControllerV1.RegistryPullSecretName(workspaceID),
		Namespace: testNamespace,
	}
	return r.client.Create(ctx, secret)
}

func newTokenModeApplier(t *testing.T, issueErr error) (*Applier, *recordingPullSecrets) {
	t.Helper()
	a := newTestApplier(t)
	pulls := &recordingPullSecrets{client: a.client, err: issueErr}
	a.pullSecrets = pulls
	return a, pulls
}

func workspacePlacement(t *testing.T, workspaceID string) Placement {
	t.Helper()
	p := placement(t, testPlacementID, 1)
	p.Application = marshalPayload(t, &locoControllerV1.ApplicationSpec{
		Type:        testAppType,
		ResourceID:  testResourceID,
		WorkspaceID: workspaceID,
	})
	return p
}

func TestTheFirstPlacementOfAWorkspaceWritesItsPullSecretBeforeTheApplication(t *testing.T) {
	a, pulls := newTokenModeApplier(t, nil)

	if err := a.ApplyPlacement(context.Background(), workspacePlacement(t, testWorkspaceID)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(pulls.workspaces) != 1 || pulls.workspaces[0] != testWorkspaceID {
		t.Fatalf("ensured workspaces = %v, want [%s]", pulls.workspaces, testWorkspaceID)
	}
	if pulls.appExisted {
		t.Fatal("the Application existed before its workspace's pull Secret was written")
	}
	if _, err := a.live(t); err != nil {
		t.Fatalf("get Application: %v", err)
	}
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: testNamespace, Name: locoControllerV1.RegistryPullSecretName(testWorkspaceID)}
	if err := a.client.Get(context.Background(), key, secret); err != nil {
		t.Fatalf("get pull Secret: %v", err)
	}
}

func TestAPullCredentialFailureFailsTheApplyAndLeavesNoApplication(t *testing.T) {
	a, _ := newTokenModeApplier(t, errIssueRefused)

	err := a.ApplyPlacement(context.Background(), workspacePlacement(t, testWorkspaceID))
	if !errors.Is(err, errIssueRefused) {
		t.Fatalf("apply error = %v, want the issue error", err)
	}
	if _, getErr := a.live(t); !apierrors.IsNotFound(getErr) {
		t.Fatalf("Application after a failed issue: err = %v, want not found", getErr)
	}
}

func TestTokenModeRejectsAnApplicationWithoutAWorkspace(t *testing.T) {
	a, pulls := newTokenModeApplier(t, nil)

	err := a.ApplyPlacement(context.Background(), workspacePlacement(t, ""))
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("apply error = %v, want ErrInvalidPayload", err)
	}
	if len(pulls.workspaces) != 0 {
		t.Fatalf("ensured workspaces = %v, want none", pulls.workspaces)
	}
}

func TestStaticModeWritesNoPullSecret(t *testing.T) {
	a := newTestApplier(t)

	if err := a.ApplyPlacement(context.Background(), workspacePlacement(t, testWorkspaceID)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	secrets := &corev1.SecretList{}
	if err := a.client.List(context.Background(), secrets, client.InNamespace(testNamespace)); err != nil {
		t.Fatalf("list Secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Fatalf("Secrets in static mode = %d, want none", len(secrets.Items))
	}
}
