package installation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	envDatabaseURL      = "DATABASE_URL"
	envSourceAccess     = "LOCO_SOURCE_BUCKET_ACCESS_KEY_ID"
	envSourceSecret     = "LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY"
	envRegistryUser     = "LOCO_REGISTRY_USERNAME"
	envRegistryPassword = "LOCO_REGISTRY_PASSWORD"
	envEmailHookSecret  = "EMAIL_HOOK_SECRET" //nolint:gosec // Environment variable name, not a credential.
	emailHookSecretKey  = "hook-secret"
	// standard Webhooks wire format, also checked by api/webhooks.decodeSecret.
	emailHookSecretBytes    = 32
	sealKeyBytes            = 32
	secretKeyField          = "key"
	secretPasswordField     = "password"
	secretUsernameField     = "username"
	minimumCredentialLength = 32
	stateTrue               = "true"
	tierProduction          = "production"

	helmInstallTimeout  = "20m"
	crdReadyTimeout     = "120s"
	controlPlaneRelease = "loco-control-plane"
	sealSecretName      = "loco-openbao-seal" //nolint:gosec // Kubernetes Secret name, not key material.
	// the pinned ClickHouse subchart's native service port is not configurable
	clickHouseNativePort = "9000"
)

var errInputMissing = errors.New("required input Secret or key is missing")
var errEmailTransport = errors.New("email transport input is invalid")
var errEmailHookSecret = errors.New("HTTP email transport requires a valid hook-secret in its input Secret")

var errRecoveryRequired = errors.New(
	"generated credentials are missing beside an existing control plane; restore the original Secret",
)

// Plan contains only non-secret installation observations.
type Plan struct {
	Operations []string `json:"operations"`
	Blocked    []string `json:"blocked,omitempty"`
}

type releaseArtifacts struct {
	manifest     Manifest
	controlPlane []byte
	worker       []byte
	certManager  []byte
	crds         [][]byte
	clickhouse   managedClickHouse
}

func loadArtifacts(ctx context.Context, path string) (releaseArtifacts, error) {
	manifest, err := LoadManifest(path)
	if err != nil {
		return releaseArtifacts{}, err
	}
	result := releaseArtifacts{manifest: manifest}
	for _, item := range []struct {
		key    string
		target *[]byte
	}{
		{"loco-control-plane", &result.controlPlane},
		{"loco-worker", &result.worker},
		{"cert-manager-chart", &result.certManager},
	} {
		*item.target, err = artifact(ctx, manifest.Artifacts[item.key])
		if err != nil {
			return releaseArtifacts{}, fmt.Errorf("artifact %s: %w", item.key, err)
		}
	}
	result.clickhouse, err = readManagedClickHouse(result.controlPlane)
	if err != nil {
		return releaseArtifacts{}, err
	}
	for _, key := range []string{"loco-gateway-crd", "loco-applications-crd", "loco-builds-crd"} {
		data, err := artifact(ctx, manifest.Artifacts[key])
		if err != nil {
			return releaseArtifacts{}, err
		}
		files, err := archiveFiles(data)
		if err != nil {
			return releaseArtifacts{}, err
		}
		for name, data := range files {
			if strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, "kustomization.yaml") {
				result.crds = append(result.crds, data)
			}
		}
	}
	return result, nil
}

func PlanInstallation(ctx context.Context, config Config, manifestPath string) (Plan, error) {
	release, err := loadArtifacts(ctx, manifestPath)
	if err != nil {
		return Plan{}, err
	}
	if renderErr := renderInstallation(ctx, config, release); renderErr != nil {
		return Plan{}, renderErr
	}
	if err = preflightTargets(ctx, config, release); err != nil {
		return Plan{}, err
	}
	kube, err := NewKubernetes(config.ControlPlane.Kubernetes)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Operations: []string{
			"validate target ownership and prerequisites",
			"preserve or create internal credentials",
			"install pinned control-plane release",
			"bootstrap first administrator",
			"attach configured workers",
		},
	}
	if _, err = kube.clusterUID(ctx); err != nil {
		return Plan{}, err
	}
	state, err := kube.Core.ConfigMaps(kube.Target.Namespace).Get(ctx, installationState, metav1.GetOptions{})
	if err == nil {
		if state.Labels[installationOwner] != config.Name {
			return Plan{}, errForeignOwnership
		}
		if _, err = kube.state(ctx, config.Name); err != nil {
			return Plan{}, err
		}
	} else if !apierrors.IsNotFound(err) {
		return Plan{}, safeKubernetesError("read installation state", err)
	}
	if _, err := kube.inputs(ctx, config); err != nil {
		plan.Blocked = append(plan.Blocked, err.Error())
	}
	if err := prerequisites(ctx, kube, release, false); err != nil {
		plan.Blocked = append(plan.Blocked, err.Error())
	}
	if err := validateIssuer(ctx, kube, config.ControlPlane.Certificates.IssuerRef); err != nil {
		plan.Blocked = append(plan.Blocked, err.Error())
	}
	for _, worker := range config.Workers {
		target, targetErr := NewKubernetes(worker.Kubernetes)
		if targetErr != nil {
			return Plan{}, targetErr
		}
		if prerequisiteErr := prerequisites(ctx, target, release, false); prerequisiteErr != nil {
			plan.Blocked = append(plan.Blocked, fmt.Sprintf("worker %s: %v", worker.Name, prerequisiteErr))
		}
		if issuerErr := validateIssuer(ctx, target, worker.Certificates.IssuerRef); issuerErr != nil {
			plan.Blocked = append(plan.Blocked, fmt.Sprintf("worker %s: %v", worker.Name, issuerErr))
		}
	}
	return plan, nil
}

// ApplyControlPlane stops at the human authentication checkpoint. It requires no
// API session to establish the control plane itself.
func ApplyControlPlane(ctx context.Context, config Config, manifestPath string) error {
	release, err := loadArtifacts(ctx, manifestPath)
	if err != nil {
		return err
	}
	if renderErr := renderInstallation(ctx, config, release); renderErr != nil {
		return renderErr
	}
	if err = preflightTargets(ctx, config, release); err != nil {
		return err
	}
	kube, err := NewKubernetes(config.ControlPlane.Kubernetes)
	if err != nil {
		return err
	}
	inputs, err := kube.inputs(ctx, config)
	if err != nil {
		return err
	}
	state, err := kube.establish(ctx, config.Name, "")
	if err != nil {
		return err
	}
	return kube.withLock(ctx, state, func(ctx context.Context, lockedState *corev1.ConfigMap) error {
		if releaseErr := validateRecordedRelease(lockedState.Data, release.manifest); releaseErr != nil {
			return releaseErr
		}
		if err := kube.ensureCredentials(ctx, lockedState, inputs, release.clickhouse); err != nil {
			return err
		}
		lockedState.Data["credentialsEstablished"] = stateTrue
		lockedState.Data["releaseVersion"] = release.manifest.Version
		lockedState.Data["releaseCommit"] = release.manifest.CoreCommit
		var updateErr error
		lockedState, updateErr = kube.Core.ConfigMaps(kube.Target.Namespace).
			Update(ctx, lockedState, metav1.UpdateOptions{})
		if updateErr != nil {
			return safeKubernetesError("record credential establishment", updateErr)
		}
		if err := kube.applyCRDs(ctx, release.crds); err != nil {
			return err
		}
		if err := prerequisites(ctx, kube, release, true); err != nil {
			return err
		}
		if err := validateIssuer(ctx, kube, config.ControlPlane.Certificates.IssuerRef); err != nil {
			return err
		}
		values, valuesErr := controlPlaneValues(
			config,
			release.manifest,
			lockedState.Data["bootstrapComplete"] == stateTrue,
		)
		if valuesErr != nil {
			return valuesErr
		}
		if err := installChart(ctx, kube.Target, controlPlaneRelease, release.controlPlane, values); err != nil {
			return err
		}
		return nil
	})
}

func CompleteBootstrap(ctx context.Context, config Config, manifestPath string) error {
	release, err := loadArtifacts(ctx, manifestPath)
	if err != nil {
		return err
	}
	kube, err := NewKubernetes(config.ControlPlane.Kubernetes)
	if err != nil {
		return err
	}
	state, err := kube.state(ctx, config.Name)
	if err != nil {
		return err
	}
	return kube.withLock(ctx, state, func(ctx context.Context, lockedState *corev1.ConfigMap) error {
		if releaseErr := validateRecordedRelease(lockedState.Data, release.manifest); releaseErr != nil {
			return releaseErr
		}
		values, valuesErr := controlPlaneValues(config, release.manifest, true)
		if valuesErr != nil {
			return valuesErr
		}
		if err := installChart(
			ctx,
			kube.Target,
			controlPlaneRelease,
			release.controlPlane,
			values,
		); err != nil {
			return err
		}
		// Persist completion before deleting the temporary credential, so a resumed apply
		// cannot reintroduce it after a crash or a lost acknowledgement.
		lockedState.Data["bootstrapComplete"] = stateTrue
		if _, err := kube.Core.ConfigMaps(kube.Target.Namespace).
			Update(ctx, lockedState, metav1.UpdateOptions{}); err != nil {
			return safeKubernetesError("record bootstrap completion", err)
		}
		secret, err := kube.Core.Secrets(kube.Target.Namespace).Get(ctx, BootstrapSecret, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return safeKubernetesError("read bootstrap credential", err)
		}
		if secret.Annotations[installationID] != string(lockedState.UID) {
			return errForeignOwnership
		}
		uid := secret.UID
		if err := kube.Core.Secrets(kube.Target.Namespace).
			Delete(ctx, BootstrapSecret, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil &&
			!apierrors.IsNotFound(err) {
			return safeKubernetesError("remove bootstrap credential", err)
		}
		return nil
	})
}

func (k *Kubernetes) inputs(ctx context.Context, config Config) (map[string][]byte, error) {
	cp := config.ControlPlane
	output := map[string][]byte{}
	secrets := map[string]*corev1.Secret{}
	for _, item := range []struct{ secret, key, env string }{
		{cp.Database.SecretRef.Name, cp.Database.SecretRef.Key, envDatabaseURL},
		{cp.SourceBucket.CredentialsSecretRef.Name, "access-key-id", envSourceAccess},
		{cp.SourceBucket.CredentialsSecretRef.Name, "secret-access-key", envSourceSecret},
		{cp.Registry.CredentialsSecretRef.Name, "api-username", envRegistryUser},
		{cp.Registry.CredentialsSecretRef.Name, "api-password", envRegistryPassword},
		{cp.Registry.CredentialsSecretRef.Name, "push-username", "push-username"},
		{cp.Registry.CredentialsSecretRef.Name, "push-password", "push-password"},
		{cp.Registry.CredentialsSecretRef.Name, "pull-username", "pull-username"},
		{cp.Registry.CredentialsSecretRef.Name, "pull-password", "pull-password"},
		{cp.Email.TransportSecretRef.Name, cp.Email.TransportSecretRef.Key, "email-transport"},
		{cp.OpenBao.Snapshots.CredentialsSecretRef.Name, "access-key-id", "snapshot-access"},
		{cp.OpenBao.Snapshots.CredentialsSecretRef.Name, "secret-access-key", "snapshot-secret"},
	} {
		secret, found := secrets[item.secret]
		if !found {
			var err error
			secret, err = k.Core.Secrets(k.Target.Namespace).Get(ctx, item.secret, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("input Secret %s: %w", item.secret, errInputMissing)
			}
			if err != nil {
				return nil, safeKubernetesError("read input Secret", err)
			}
			secrets[item.secret] = secret
		}
		value := secret.Data[item.key]
		if len(value) == 0 {
			return nil, fmt.Errorf("input Secret %s key %s: %w", item.secret, item.key, errInputMissing)
		}
		output[item.env] = value
	}
	transport, err := url.Parse(string(output["email-transport"]))
	if err != nil || transport.Hostname() == "" {
		return nil, errEmailTransport
	}
	if transport.Scheme == "smtp" {
		if transport.Port() == "" {
			return nil, errEmailTransport
		}
		return output, nil
	}
	if transport.Scheme != schemeHTTP && transport.Scheme != schemeHTTPS {
		return nil, errEmailTransport
	}
	hook := secrets[cp.Email.TransportSecretRef.Name].Data[emailHookSecretKey]
	encoded, prefixed := strings.CutPrefix(string(hook), "whsec_")
	decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
	if !prefixed || decodeErr != nil || len(decoded) != emailHookSecretBytes {
		return nil, errEmailHookSecret
	}
	output[envEmailHookSecret] = hook
	return output, nil
}

func (k *Kubernetes) ensureCredentials(
	ctx context.Context, state *corev1.ConfigMap, inputs map[string][]byte, clickhouse managedClickHouse,
) error {
	existing, err := k.Core.Pods(k.Target.Namespace).
		List(ctx, metav1.ListOptions{LabelSelector: "app=" + controlPlaneRelease + "-api"})
	if err != nil {
		return safeKubernetesError("inspect existing control plane", err)
	}
	names := []string{
		sealSecretName,
		"loco-api-env",
		"loco-clickhouse-migrator",
		"loco-clickhouse-ingest",
		"loco-clickhouse-reader",
		clickhouse.OperatorName,
	}
	if state.Data["bootstrapComplete"] != stateTrue {
		names = append(names, BootstrapSecret)
	}
	for _, name := range names {
		_, err = k.Core.Secrets(k.Target.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) && (len(existing.Items) > 0 || state.Data["credentialsEstablished"] == stateTrue) {
			return errRecoveryRequired
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return safeKubernetesError("inspect generated credential", err)
		}
	}
	seal := make([]byte, sealKeyBytes)
	if _, err = rand.Read(seal); err != nil {
		return err
	}
	sealSecret, err := k.ensureSecret(
		ctx,
		state,
		sealSecretName,
		[]string{secretKeyField},
		map[string][]byte{secretKeyField: []byte(base64.StdEncoding.EncodeToString(seal))},
	)
	if err != nil {
		return err
	}
	if !validSeal(sealSecret) {
		return errSecretInvalid
	}
	apiKeys := []string{envDatabaseURL, envSourceAccess, envSourceSecret, envRegistryUser, envRegistryPassword}
	if _, hook := inputs[envEmailHookSecret]; hook {
		apiKeys = append(apiKeys, envEmailHookSecret)
	}
	apiData := map[string][]byte{}
	for _, key := range apiKeys {
		apiData[key] = inputs[key]
	}
	apiSecret, err := k.ensureSecret(ctx, state, "loco-api-env", apiKeys, apiData)
	if err != nil {
		return err
	}
	for key, value := range apiData {
		if !bytes.Equal(apiSecret.Data[key], value) {
			return errSecretInvalid
		}
	}
	if state.Data["bootstrapComplete"] != stateTrue {
		token, err := randomCredential()
		if err != nil {
			return err
		}
		bootstrap, tokenErr := k.ensureSecret(
			ctx,
			state,
			BootstrapSecret,
			[]string{BootstrapTokenKey},
			map[string][]byte{BootstrapTokenKey: token},
		)
		if tokenErr != nil {
			return tokenErr
		}
		if len(bootstrap.Data[BootstrapTokenKey]) < minimumCredentialLength {
			return errSecretInvalid
		}
	}
	if err := k.ensureClickHouseOperator(ctx, state, clickhouse); err != nil {
		return err
	}
	// Altinity's default installation Service name is clickhouse-<CHI name>.
	service := "clickhouse-" + clickhouse.Name + "." + k.Target.Namespace + ".svc.cluster.local"
	host := net.JoinHostPort(service, clickHouseNativePort)
	for _, role := range []string{"migrator", "ingest", "reader"} {
		password, err := randomCredential()
		if err != nil {
			return err
		}
		dsn := fmt.Sprintf(
			"clickhouse://loco_%s:%s@%s",
			role,
			password,
			host,
		)
		clickhouse, secretErr := k.ensureSecret(
			ctx,
			state,
			"loco-clickhouse-"+role,
			[]string{secretPasswordField, "url"},
			map[string][]byte{secretPasswordField: password, "url": []byte(dsn)},
		)
		if secretErr != nil {
			return secretErr
		}
		if !validClickHouse(clickhouse, role, host) {
			return errSecretInvalid
		}
	}
	return nil
}

func chartCommand(
	ctx context.Context,
	target KubernetesTarget,
	name string,
	chart []byte,
	values []byte,
	install bool,
) error {
	directory, err := os.MkdirTemp("", "locoadm-chart-")
	if err != nil {
		return fmt.Errorf("create release workspace: %w", err)
	}
	defer os.RemoveAll(directory)
	chartPath := filepath.Join(directory, "chart.tgz")
	if err = os.WriteFile(chartPath, chart, 0600); err != nil {
		return fmt.Errorf("write public release artifact: %w", err)
	}
	if !install {
		return command(
			ctx,
			"render "+name,
			values,
			"helm",
			"template",
			name,
			chartPath,
			"--namespace",
			target.Namespace,
			"--values",
			"-",
			"--validate=false",
			"--skip-tests",
		)
	}
	return command(
		ctx,
		"install "+name,
		values,
		"helm",
		"upgrade",
		"--install",
		name,
		chartPath,
		"--kube-context",
		target.Context,
		"--namespace",
		target.Namespace,
		"--values",
		"-",
		"--wait",
		"--wait-for-jobs",
		"--timeout",
		helmInstallTimeout,
	)
}

func (k *Kubernetes) applyCRDs(ctx context.Context, manifests [][]byte) error {
	for _, manifest := range manifests {
		for _, step := range []struct {
			operation string
			args      []string
		}{
			{"apply pinned CRDs", []string{"apply", "--server-side", "--field-manager=locoadm", "-f", "-"}},
			{"wait pinned CRDs", []string{"wait", "--for=condition=Established", "--timeout=" + crdReadyTimeout, "-f", "-"}},
		} {
			args := append([]string{"--context", k.Target.Context}, step.args...)
			if err := command(ctx, step.operation, manifest, "kubectl", args...); err != nil {
				return err
			}
		}
	}
	return nil
}

func command(ctx context.Context, operation string, input []byte, program string, args ...string) error {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = bytes.NewReader(input)
	// Child diagnostics may contain Secret values, RPC payloads or transport URLs.
	// Only the bounded operation label and exit status cross the output boundary.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: subprocess failed: %w", operation, err)
	}
	return nil
}

func BootstrapComplete(ctx context.Context, config Config) (bool, error) {
	kube, err := NewKubernetes(config.ControlPlane.Kubernetes)
	if err != nil {
		return false, err
	}
	state, err := kube.state(ctx, config.Name)
	if err != nil {
		return false, err
	}
	return state.Data["bootstrapComplete"] == stateTrue, nil
}

func installChart(ctx context.Context, target KubernetesTarget, name string, chart, values []byte) error {
	return chartCommand(ctx, target, name, chart, values, true)
}

func renderChart(ctx context.Context, target KubernetesTarget, name string, chart, values []byte) error {
	return chartCommand(ctx, target, name, chart, values, false)
}
