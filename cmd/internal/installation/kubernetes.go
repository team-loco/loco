package installation

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsclient "k8s.io/client-go/kubernetes/typed/apps/v1"
	authclient "k8s.io/client-go/kubernetes/typed/authorization/v1"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	credentialRandomBytes = 32
	lockExpiryMargin      = time.Minute
	lockCleanupTimeout    = 10 * time.Second
	BootstrapSecret       = "loco-bootstrap"
	BootstrapTokenKey     = "token"
	installationState     = "loco-installation"
	installationOwner     = "loco.build/installation"
	installationID        = "loco.build/installation-id"
)

var (
	errKubernetes            = errors.New("Kubernetes operation failed")
	errForeignOwnership      = errors.New("resource belongs to another installation")
	errSecretInvalid         = errors.New("existing secret is incomplete; restore its original material")
	ErrBootstrapNotFound     = errors.New("bootstrap credential is missing")
	errInstallationMissing   = errors.New("installation state is missing; run locoadm apply first")
	errTargetChanged         = errors.New("Kubernetes target UID differs from the recorded installation")
	errServerClockMissing    = errors.New("Kubernetes API server did not provide a clock sample")
	errDeadlineRequired      = errors.New("installation apply requires a deadline")
	errApplyLocked           = errors.New("another locoadm apply holds the installation lock")
	errWorkerNamespacePolicy = errors.New("existing worker namespace must match the restricted Pod Security policy")
	errLoadKubernetes        = fmt.Errorf("load Kubernetes context: %w", errKubernetes)
	errInitKubernetes        = fmt.Errorf("initialize Kubernetes client: %w", errKubernetes)
	errInitKubernetesApps    = fmt.Errorf("initialize Kubernetes apps client: %w", errKubernetes)
	errInitKubernetesAuth    = fmt.Errorf("initialize Kubernetes authorization client: %w", errKubernetes)
)

// Kubernetes uses destination state rather than local credentials or phase files.
type Kubernetes struct {
	Core          *coreclient.CoreV1Client
	Apps          *appsclient.AppsV1Client
	Authorization *authclient.AuthorizationV1Client
	Target        KubernetesTarget
}

func NewKubernetes(target KubernetesTarget) (*Kubernetes, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{CurrentContext: target.Context}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides).
		ClientConfig()
	if err != nil {
		return nil, errLoadKubernetes
	}
	core, err := coreclient.NewForConfig(config)
	if err != nil {
		return nil, errInitKubernetes
	}
	apps, err := appsclient.NewForConfig(config)
	if err != nil {
		return nil, errInitKubernetesApps
	}
	authorization, authErr := authclient.NewForConfig(config)
	if authErr != nil {
		return nil, errInitKubernetesAuth
	}
	return &Kubernetes{Core: core, Apps: apps, Authorization: authorization, Target: target}, nil
}

func (k *Kubernetes) ReadBootstrapToken(ctx context.Context, name string) (string, error) {
	if _, targetErr := k.clusterUID(ctx); targetErr != nil {
		return "", targetErr
	}
	state, err := k.Core.ConfigMaps(k.Target.Namespace).Get(ctx, installationState, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", errInstallationMissing
	}
	if err != nil {
		return "", safeKubernetesError("read installation state", err)
	}
	if state.Labels[installationOwner] != name {
		return "", errForeignOwnership
	}
	secret, err := k.Core.Secrets(k.Target.Namespace).Get(ctx, BootstrapSecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", ErrBootstrapNotFound
	}
	if err != nil {
		return "", safeKubernetesError("read bootstrap credential", err)
	}
	if secret.Annotations[installationID] != string(state.UID) {
		return "", errForeignOwnership
	}
	if len(secret.Data[BootstrapTokenKey]) < minimumCredentialLength {
		return "", errSecretInvalid
	}
	return string(secret.Data[BootstrapTokenKey]), nil
}

func (k *Kubernetes) clusterUID(ctx context.Context) (string, error) {
	namespace, err := k.Core.Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return "", safeKubernetesError("read Kubernetes target UID", err)
	}
	if string(namespace.UID) != k.Target.ClusterUID {
		return "", errTargetChanged
	}
	return string(namespace.UID), nil
}

func (k *Kubernetes) state(ctx context.Context, name string) (*corev1.ConfigMap, error) {
	uid, err := k.clusterUID(ctx)
	if err != nil {
		return nil, err
	}
	state, err := k.Core.ConfigMaps(k.Target.Namespace).Get(ctx, installationState, metav1.GetOptions{})
	if err != nil {
		return nil, safeKubernetesError("read installation state", err)
	}
	if state.Labels[installationOwner] != name {
		return nil, errForeignOwnership
	}
	if state.Data["clusterUID"] != uid {
		return nil, errTargetChanged
	}
	return state, nil
}

func (k *Kubernetes) establish(ctx context.Context, name, controlPlaneID string) (*corev1.ConfigMap, error) {
	uid, err := k.clusterUID(ctx)
	if err != nil {
		return nil, err
	}
	var namespaceLabels map[string]string
	if controlPlaneID != "" {
		if policyErr := k.checkWorkerNamespace(ctx); policyErr != nil {
			return nil, policyErr
		}
		namespaceLabels = workerNamespaceLabels()
	}
	_, err = k.Core.Namespaces().
		Create(ctx, &corev1.Namespace{Name: k.Target.Namespace, Labels: namespaceLabels}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, safeKubernetesError("create installation namespace", err)
	}
	if controlPlaneID != "" {
		if policyErr := k.checkWorkerNamespace(ctx); policyErr != nil {
			return nil, policyErr
		}
	}
	_, err = k.Core.ConfigMaps(k.Target.Namespace).Create(ctx, &corev1.ConfigMap{
		Name: installationState, Labels: map[string]string{installationOwner: name},
		Data: map[string]string{"clusterUID": uid, "controlPlaneID": controlPlaneID},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, safeKubernetesError("create installation state", err)
	}
	state, stateErr := k.state(ctx, name)
	if stateErr != nil {
		return nil, stateErr
	}
	if state.Data["controlPlaneID"] != controlPlaneID {
		return nil, errForeignOwnership
	}
	return state, nil
}

// ensureSecret never updates an existing Secret, including after a lost create response.
func (k *Kubernetes) ensureSecret(
	ctx context.Context,
	state *corev1.ConfigMap,
	name string,
	keys []string,
	data map[string][]byte,
) (*corev1.Secret, error) {
	secret, err := k.Core.Secrets(k.Target.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		secretType := corev1.SecretTypeOpaque
		if _, docker := data[corev1.DockerConfigJsonKey]; docker {
			secretType = corev1.SecretTypeDockerConfigJson
		}
		secret = &corev1.Secret{
			Name:        name,
			Labels:      state.Labels,
			Annotations: map[string]string{installationID: string(state.UID)},
			Type:        secretType,
			Data:        data,
		}
		created, createErr := k.Core.Secrets(k.Target.Namespace).Create(ctx, secret, metav1.CreateOptions{})
		if createErr == nil {
			secret, err = created, nil
		} else {
			// Read again even after an ambiguous transport failure; creation may have succeeded.
			secret, err = k.Core.Secrets(k.Target.Namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, safeKubernetesError("create installation secret", createErr)
			}
		}
	}
	if err != nil {
		return nil, safeKubernetesError("read installation secret", err)
	}
	if secret.Annotations[installationID] != string(state.UID) {
		return nil, errForeignOwnership
	}
	for _, key := range keys {
		if len(secret.Data[key]) == 0 {
			return nil, errSecretInvalid
		}
	}
	return secret, nil
}

func randomCredential() ([]byte, error) {
	data := make([]byte, credentialRandomBytes)
	if _, err := rand.Read(data); err != nil {
		return nil, fmt.Errorf("generate installation credential: %w", err)
	}
	return []byte(base64.RawURLEncoding.EncodeToString(data)), nil
}

// safeKubernetesError excludes server messages, which can echo submitted Secret data.
func safeKubernetesError(operation string, err error) error {
	reason := apierrors.ReasonForError(err)
	allowed := []metav1.StatusReason{
		metav1.StatusReasonForbidden, metav1.StatusReasonUnauthorized, metav1.StatusReasonNotFound,
		metav1.StatusReasonAlreadyExists, metav1.StatusReasonConflict, metav1.StatusReasonInvalid,
		metav1.StatusReasonTimeout, metav1.StatusReasonServerTimeout, metav1.StatusReasonInternalError,
		metav1.StatusReasonServiceUnavailable,
	}
	if !slices.Contains(allowed, reason) {
		reason = metav1.StatusReasonUnknown
	}
	return fmt.Errorf("%s (%s): %w", operation, reason, errKubernetes)
}

// withLock serializes installation mutations using atomic ConfigMap creation. The
// lease uses API-server time so operator clock skew cannot expire a live holder.
func (k *Kubernetes) withLock(
	ctx context.Context,
	state *corev1.ConfigMap,
	run func(context.Context, *corev1.ConfigMap) error,
) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errDeadlineRequired
	}
	remaining := time.Until(deadline)
	locks := k.Core.ConfigMaps(k.Target.Namespace)
	clock, err := locks.Create(ctx, &corev1.ConfigMap{GenerateName: "loco-installation-clock-"},
		metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	if err != nil {
		return safeKubernetesError("sample API-server clock", err)
	}
	if clock.CreationTimestamp.IsZero() {
		return errServerClockMissing
	}
	lock := &corev1.ConfigMap{
		Name:        "loco-installation-lock",
		Annotations: map[string]string{installationID: string(state.UID)},
		Data: map[string]string{
			"expires": clock.CreationTimestamp.Add(remaining + lockExpiryMargin).UTC().Format(time.RFC3339),
		},
	}
	held, err := locks.Create(ctx, lock, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		existing, getErr := locks.Get(ctx, lock.Name, metav1.GetOptions{})
		if getErr != nil {
			return safeKubernetesError("read installation lock", getErr)
		}
		expires, parseErr := time.Parse(time.RFC3339, existing.Data["expires"])
		if parseErr != nil || existing.Annotations[installationID] != string(state.UID) {
			return errForeignOwnership
		}
		if clock.CreationTimestamp.Time.Before(expires) {
			return errApplyLocked
		}
		lock.ResourceVersion = existing.ResourceVersion
		held, err = locks.Update(ctx, lock, metav1.UpdateOptions{})
	}
	if err != nil {
		return safeKubernetesError("acquire installation lock", err)
	}
	freshState, runErr := k.state(ctx, state.Labels[installationOwner])
	if runErr == nil {
		runErr = run(ctx, freshState)
	}
	// Cleanup has a separate short context because apply may have reached its deadline.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockCleanupTimeout)
	defer cancel()
	uid := held.UID
	deleteErr := locks.Delete(
		cleanup,
		lock.Name,
		metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &held.ResourceVersion}},
	)
	if runErr != nil {
		return runErr
	}
	if deleteErr != nil {
		return safeKubernetesError("release installation lock", deleteErr)
	}
	return nil
}
