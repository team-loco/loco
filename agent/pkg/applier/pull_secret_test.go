package applier

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/agent/pkg/registrycred"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const testWorkspaceID = "01a33333-3333-7333-8333-333333333333"

type recordingIssuer struct {
	registryv1connect.UnimplementedRegistryServiceHandler
	client     client.Client
	refuse     bool
	workspaces []string
	appExisted bool
}

func (r *recordingIssuer) IssuePullCredential(
	ctx context.Context,
	req *registryv1.IssuePullCredentialRequest,
) (*registryv1.IssuePullCredentialResponse, error) {
	r.workspaces = append(r.workspaces, req.GetWorkspaceId())
	app := &locoControllerV1.Application{}
	key := client.ObjectKey{Namespace: testNamespace, Name: locoControllerV1.ApplicationName(testPlacementID)}
	if err := r.client.Get(ctx, key, app); !apierrors.IsNotFound(err) {
		r.appExisted = true
	}
	if r.refuse {
		return nil, connect.NewError(connect.CodeUnavailable, "control plane unavailable")
	}
	return &registryv1.IssuePullCredentialResponse{
		Username:     locoControllerV1.RegistryPullUsername,
		Password:     "secret",
		ExpiresAt:    timestamppb.New(time.Now().Add(time.Hour)),
		RegistryHost: "registry.loco.test:5000",
	}, nil
}

func newTokenModeApplier(t *testing.T, refuse bool) (*Applier, *recordingIssuer) {
	t.Helper()
	a := newTestApplier(t)
	issuer := &recordingIssuer{client: a.client, refuse: refuse}
	server := connect.NewServer()
	registryv1connect.RegisterRegistryServiceHandler(server, issuer)
	rpc := registryv1connect.NewRegistryServiceClient(connect.NewClient(connectinprocess.New(server)))
	a.pullSecrets = registrycred.NewWithClient(a.client, rpc, testNamespace, time.Hour, nil)
	return a, issuer
}

func workspacePlacement(t *testing.T, workspaceID string) Placement {
	t.Helper()
	p := placement(t, testPlacementID, 1)
	p.Application = marshalPayload(t, &locoControllerV1.ApplicationSpec{
		Type:          testAppType,
		ResourceID:    testResourceID,
		EnvironmentID: testEnvironmentID,
		WorkspaceID:   workspaceID,
	})
	return p
}

func TestTheFirstPlacementOfAWorkspaceWritesItsPullSecretBeforeTheApplication(t *testing.T) {
	a, issuer := newTokenModeApplier(t, false)

	if err := a.ApplyPlacement(context.Background(), workspacePlacement(t, testWorkspaceID)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(issuer.workspaces) != 1 || issuer.workspaces[0] != testWorkspaceID {
		t.Fatalf("ensured workspaces = %v, want [%s]", issuer.workspaces, testWorkspaceID)
	}
	if issuer.appExisted {
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
	if secret.Labels[locoControllerV1.LabelRegistryPull] != testWorkspaceID ||
		secret.Type != corev1.SecretTypeDockerConfigJson {
		t.Fatalf("pull Secret labels = %v, type = %s, want the workspace's docker config", secret.Labels, secret.Type)
	}
}

func TestAPullCredentialFailureFailsTheApplyAndLeavesNoApplication(t *testing.T) {
	a, _ := newTokenModeApplier(t, true)

	err := a.ApplyPlacement(context.Background(), workspacePlacement(t, testWorkspaceID))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("apply error = %v, want the issue error", err)
	}
	if _, getErr := a.live(t); !apierrors.IsNotFound(getErr) {
		t.Fatalf("Application after a failed issue: err = %v, want not found", getErr)
	}
}

func TestTokenModeRejectsAnApplicationWithoutAWorkspace(t *testing.T) {
	a, issuer := newTokenModeApplier(t, false)

	err := a.ApplyPlacement(context.Background(), workspacePlacement(t, ""))
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("apply error = %v, want ErrInvalidPayload", err)
	}
	if len(issuer.workspaces) != 0 {
		t.Fatalf("ensured workspaces = %v, want none", issuer.workspaces)
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
