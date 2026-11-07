package registrycred

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/agent/pkg/kube"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	renewalDivisor = 2
	authSeparator  = ":"
)

var (
	errNoRegistryHost = errors.New("the control plane named no registry host")
	errNoPassword     = errors.New("the control plane issued an empty registry pull credential")
	errNoLifetime     = errors.New("the control plane issued a registry pull credential that is already expired")
)

type dockerAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

type dockerConfig struct {
	Auths map[string]dockerAuth `json:"auths"`
}

func DockerConfig(host, username, password string) ([]byte, error) {
	encoded := base64.StdEncoding.EncodeToString([]byte(username + authSeparator + password))
	config := dockerConfig{Auths: map[string]dockerAuth{
		host: {Username: username, Password: password, Auth: encoded},
	}}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode docker config: %w", err)
	}
	return data, nil
}

type Manager struct {
	client    client.Client
	issuer    registryv1connect.RegistryServiceClient
	namespace string
	interval  time.Duration
	backoff   func() wait.Backoff
	now       func() time.Time

	mu sync.Mutex
}

func New(
	restConfig *rest.Config,
	issuer registryv1connect.RegistryServiceClient,
	namespace string,
	interval time.Duration,
	backoff func() wait.Backoff,
) (*Manager, error) {
	scheme, err := kube.NewScheme()
	if err != nil {
		return nil, err
	}
	c, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return NewWithClient(c, issuer, namespace, interval, backoff), nil
}

func NewWithClient(
	c client.Client,
	issuer registryv1connect.RegistryServiceClient,
	namespace string,
	interval time.Duration,
	backoff func() wait.Backoff,
) *Manager {
	return &Manager{
		client:    c,
		issuer:    issuer,
		namespace: namespace,
		interval:  interval,
		backoff:   backoff,
		now:       time.Now,
	}
}

func (m *Manager) Ensure(ctx context.Context, workspaceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.ensure(ctx, workspaceID)
	return err
}

func (m *Manager) ensure(ctx context.Context, workspaceID string) (*corev1.Secret, error) {
	name := locoControllerV1.RegistryPullSecretName(workspaceID)
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: m.namespace, Name: name}
	err := m.client.Get(ctx, key, secret)
	if err == nil && secret.Labels[locoControllerV1.LabelRegistryPull] == workspaceID {
		return nil, nil
	}
	if err == nil {
		return secret, m.renew(ctx, workspaceID, secret)
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("read registry pull Secret %s: %w", name, err)
	}
	secret = &corev1.Secret{Name: name, Namespace: m.namespace}
	if issueErr := m.issue(ctx, workspaceID, secret); issueErr != nil {
		return nil, issueErr
	}
	if createErr := m.client.Create(ctx, secret); createErr != nil {
		return nil, fmt.Errorf("create registry pull Secret %s: %w", name, createErr)
	}
	slog.InfoContext(ctx, "wrote registry pull Secret", "name", name, "workspace_id", workspaceID)
	return secret, nil
}

func (m *Manager) Run(ctx context.Context) {
	backoff := m.backoff()
	for {
		next, err := m.check(ctx)
		if ctx.Err() != nil {
			return
		}
		delay := m.interval
		switch {
		case connect.CodeOf(err) == connect.CodeFailedPrecondition:
			delay = m.backoff().Cap
			slog.ErrorContext(ctx, "the control plane issues no registry credentials",
				"error", err, "retry_in", delay)
		case err != nil:
			delay = backoff.Step()
			slog.WarnContext(ctx, "registry pull credential check failed", "error", err, "retry_in", delay)
		default:
			backoff = m.backoff()
			if until := next.Sub(m.now()); !next.IsZero() && until < delay {
				delay = until
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *Manager) check(ctx context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	secrets := &corev1.SecretList{}
	pullSecrets := client.HasLabels{locoControllerV1.LabelRegistryPull}
	if err := m.client.List(ctx, secrets, client.InNamespace(m.namespace), pullSecrets); err != nil {
		return time.Time{}, fmt.Errorf("list registry pull Secrets: %w", err)
	}
	apps := &locoControllerV1.ApplicationList{}
	if err := m.client.List(ctx, apps, client.InNamespace(m.namespace)); err != nil {
		return time.Time{}, fmt.Errorf("list Applications: %w", err)
	}
	placed := make(map[string]struct{}, len(apps.Items))
	for i := range apps.Items {
		if apps.Items[i].DeletionTimestamp != nil {
			continue
		}
		placed[apps.Items[i].Spec.WorkspaceID] = struct{}{}
	}
	now := m.now()
	var next time.Time
	schedule := func(secret *corev1.Secret) {
		at, ok := secretRenewAt(secret)
		if ok && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	var errs []error
	secured := make(map[string]struct{}, len(secrets.Items))
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		workspaceID := secret.Labels[locoControllerV1.LabelRegistryPull]
		secured[workspaceID] = struct{}{}
		if _, isPlaced := placed[workspaceID]; !isPlaced {
			issuedAt, issuedOK := annotationTime(secret, locoControllerV1.AnnotationRegistryPullIssuedAt)
			if issuedOK && now.Sub(issuedAt) < m.interval {
				continue
			}
			errs = append(errs, m.remove(ctx, secret))
			continue
		}
		if at, ok := secretRenewAt(secret); ok && now.Before(at) {
			schedule(secret)
			continue
		}
		renewErr := m.renew(ctx, workspaceID, secret)
		if renewErr == nil {
			schedule(secret)
		}
		errs = append(errs, refused(ctx, workspaceID, renewErr))
	}
	for workspaceID := range placed {
		if _, ok := secured[workspaceID]; ok {
			continue
		}
		written, ensureErr := m.ensure(ctx, workspaceID)
		if written != nil && ensureErr == nil {
			schedule(written)
		}
		errs = append(errs, refused(ctx, workspaceID, ensureErr))
	}
	return next, errors.Join(errs...)
}

func refused(ctx context.Context, workspaceID string, err error) error {
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		return err
	}
	slog.ErrorContext(ctx, "the control plane refused a registry pull credential",
		"workspace_id", workspaceID, "error", err)
	return nil
}

func secretRenewAt(secret *corev1.Secret) (time.Time, bool) {
	issuedAt, issuedOK := annotationTime(secret, locoControllerV1.AnnotationRegistryPullIssuedAt)
	expiresAt, expiresOK := annotationTime(secret, locoControllerV1.AnnotationRegistryPullExpiresAt)
	if !issuedOK || !expiresOK {
		return time.Time{}, false
	}
	lifetime := expiresAt.Sub(issuedAt)
	return issuedAt.Add(lifetime / renewalDivisor), true
}

func annotationTime(secret *corev1.Secret, key string) (time.Time, bool) {
	value, ok := secret.Annotations[key]
	if !ok {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func (m *Manager) remove(ctx context.Context, secret *corev1.Secret) error {
	precondition := client.Preconditions{ResourceVersion: &secret.ResourceVersion}
	err := m.client.Delete(ctx, secret, precondition)
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("delete registry pull Secret %s: %w", secret.Name, err)
	}
	slog.InfoContext(ctx, "deleted registry pull Secret of a workspace with no Application", "name", secret.Name)
	return nil
}

func (m *Manager) renew(ctx context.Context, workspaceID string, secret *corev1.Secret) error {
	if err := m.issue(ctx, workspaceID, secret); err != nil {
		return err
	}
	if err := m.client.Update(ctx, secret); err != nil {
		return fmt.Errorf("update registry pull Secret %s: %w", secret.Name, err)
	}
	slog.InfoContext(ctx, "renewed registry pull Secret", "name", secret.Name, "workspace_id", workspaceID)
	return nil
}

func (m *Manager) issue(ctx context.Context, workspaceID string, secret *corev1.Secret) error {
	issuedAt := m.now()
	request := &registryv1.IssuePullCredentialRequest{WorkspaceId: workspaceID}
	resp, err := m.issuer.IssuePullCredential(ctx, request)
	if err != nil {
		return fmt.Errorf("issue registry pull credential for workspace %s: %w", workspaceID, err)
	}
	if resp.GetRegistryHost() == "" {
		return errNoRegistryHost
	}
	if resp.GetPassword() == "" {
		return errNoPassword
	}
	expiresAt := resp.GetExpiresAt().AsTime()
	if !expiresAt.After(issuedAt) {
		return fmt.Errorf("%w: expires at %s", errNoLifetime, expiresAt)
	}
	config, err := DockerConfig(resp.GetRegistryHost(), resp.GetUsername(), resp.GetPassword())
	if err != nil {
		return err
	}
	secret.Labels = map[string]string{
		locolabels.LabelManagedBy:          kube.ManagedBy,
		locoControllerV1.LabelRegistryPull: workspaceID,
	}
	secret.Annotations = map[string]string{
		locoControllerV1.AnnotationRegistryPullIssuedAt:  issuedAt.UTC().Format(time.RFC3339Nano),
		locoControllerV1.AnnotationRegistryPullExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano),
	}
	secret.Type = corev1.SecretTypeDockerConfigJson
	secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: config}
	return nil
}
