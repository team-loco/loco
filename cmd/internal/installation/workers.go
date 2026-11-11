package installation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	workerRelease         = "loco-worker"
	workerPollInterval    = 2 * time.Second
	workerHeartbeatMaxAge = time.Minute
)

var (
	errBootstrapRequired      = errors.New("first administrator bootstrap is required")
	errWorkerState            = errors.New("worker registration, target or identity differs from installation state")
	errWorkerEnrollmentStatus = errors.New("read worker enrollment status failed")
	errWorkerRegistration     = errors.New("create or recover worker registration failed")
)

// ApplyWorkers requires an authenticated system-admin session established after
// the first-admin checkpoint. Enrollment credentials never leave destination Secrets.
func ApplyWorkers(ctx context.Context, config Config, manifestPath, token string) error {
	release, err := loadArtifacts(ctx, manifestPath)
	if err != nil {
		return err
	}
	cp, err := NewKubernetes(config.ControlPlane.Kubernetes)
	if err != nil {
		return err
	}
	state, err := cp.state(ctx, config.Name)
	if err != nil {
		return err
	}
	inputs, err := cp.inputs(ctx, config)
	if err != nil {
		return err
	}
	return cp.withLock(ctx, state, func(ctx context.Context, lockedState *corev1.ConfigMap) error {
		if releaseErr := validateRecordedRelease(lockedState.Data, release.manifest); releaseErr != nil {
			return releaseErr
		}
		if lockedState.Data["bootstrapComplete"] != stateTrue {
			return errBootstrapRequired
		}
		for _, worker := range config.Workers {
			kube, err := NewKubernetes(worker.Kubernetes)
			if err != nil {
				return err
			}
			uid, err := kube.clusterUID(ctx)
			if err != nil {
				return err
			}
			requestID, changed, targetErr := recordWorkerTarget(lockedState.Data, worker.Name, uid)
			if targetErr != nil {
				return targetErr
			}
			if changed {
				lockedState, err = cp.Core.ConfigMaps(cp.Target.Namespace).
					Update(ctx, lockedState, metav1.UpdateOptions{})
				if err != nil {
					return safeKubernetesError("record canonical worker registration request", err)
				}
			}

			workerState, err := kube.establish(ctx, config.Name, string(lockedState.UID))
			if err != nil {
				return err
			}
			if err := kube.withLock(
				ctx,
				workerState,
				func(ctx context.Context, freshWorkerState *corev1.ConfigMap) error {
					if requestErr := bindWorkerRequest(freshWorkerState.Data, requestID); requestErr != nil {
						return requestErr
					}
					return kube.applyWorker(
						ctx,
						config,
						worker,
						release,
						freshWorkerState,
						string(lockedState.UID),
						token,
						inputs,
					)
				},
			); err != nil {
				return fmt.Errorf("worker %s: %w", worker.Name, err)
			}
		}
		return nil
	})
}

func (k *Kubernetes) applyWorker(
	ctx context.Context,
	config Config,
	worker Worker,
	release releaseArtifacts,
	state *corev1.ConfigMap,
	cpID, token string,
	inputs map[string][]byte,
) error {
	if stored := state.Data["controlPlaneID"]; stored != "" && stored != cpID {
		return errForeignOwnership
	}
	if _, requestErr := uuid.Parse(state.Data["requestID"]); requestErr != nil {
		return errWorkerState
	}

	client := rpc.ForHost(config.ControlPlane.APIURL, token).ClusterAdmin
	tier := environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING
	if worker.Tier == tierProduction {
		tier = environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION
	}
	created, err := client.CreateCluster(
		ctx,
		&clusterv1.CreateClusterRequest{
			Name:                worker.Name,
			ApplicationsAllowed: worker.ApplicationsAllowed, BuildsAllowed: worker.Builds.Allowed,
			Region:    worker.Region,
			Tier:      tier,
			RequestId: state.Data["requestID"],
		},
	)
	if err != nil {
		return errWorkerRegistration
	}
	if _, updateErr := client.UpdateCluster(ctx, &clusterv1.UpdateClusterRequest{
		ClusterId:           created.GetClusterId(),
		ApplicationsAllowed: worker.ApplicationsAllowed,
		BuildsAllowed:       worker.Builds.Allowed,
	}); updateErr != nil {
		return errWorkerRegistration
	}
	if previous := state.Data["clusterID"]; previous != "" && previous != created.GetClusterId() {
		return errWorkerState
	}
	state.Data["clusterID"] = created.GetClusterId()
	state, err = k.Core.ConfigMaps(k.Target.Namespace).Update(ctx, state, metav1.UpdateOptions{})
	if err != nil {
		return safeKubernetesError("record worker registration", err)
	}
	if err := k.configureEnrollment(ctx, state, created); err != nil {
		return err
	}

	if err := k.configureRegistry(ctx, state, config.ControlPlane.Registry, inputs); err != nil {
		return err
	}

	if err := k.applyCRDs(ctx, release.crds); err != nil {
		return err
	}
	if err := prerequisites(ctx, k, release, true); err != nil {
		return err
	}
	if err := validateIssuer(ctx, k, worker.Certificates.IssuerRef); err != nil {
		return err
	}
	values, valuesErr := workerValues(config, worker, release.manifest)
	if valuesErr != nil {
		return valuesErr
	}
	if err := installChart(
		ctx,
		k.Target,
		workerRelease,
		release.worker,
		values,
	); err != nil {
		return err
	}
	timer := time.NewTicker(workerPollInterval)
	defer timer.Stop()
	for {
		listed, err := client.ListClusters(ctx, &clusterv1.ListClustersRequest{})
		if err != nil {
			return errWorkerEnrollmentStatus
		}
		for _, cluster := range listed.GetClusters() {
			if cluster.GetId() != created.GetClusterId() {
				continue
			}
			ready, readyErr := k.workerReady(ctx, cluster, worker)
			if readyErr != nil {
				return readyErr
			}
			if ready {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func registryHost(registry Registry) string {
	parsed := validatedURL(registry.Endpoint)
	return parsed.Host
}

func (k *Kubernetes) workerReady(ctx context.Context, cluster *clusterv1.Cluster, worker Worker) (bool, error) {
	identity, err := k.Core.Secrets(k.Target.Namespace).Get(ctx, workerRelease+"-agent-key", metav1.GetOptions{})
	if apierrors.IsNotFound(err) && cluster.GetAgentKeyFingerprint() == "" {
		return false, nil
	}
	if err != nil {
		return false, safeKubernetesError("read enrolled worker identity", err)
	}
	private := identity.Data["private-key"]
	if len(private) != ed25519.PrivateKeySize {
		return false, errSecretInvalid
	}
	public, ok := ed25519.PrivateKey(private).Public().(ed25519.PublicKey)
	if !ok {
		return false, errSecretInvalid
	}
	sum := sha256.Sum256(public)
	if cluster.GetAgentKeyFingerprint() == "" {
		return false, nil
	}
	if cluster.GetAgentKeyFingerprint() != hex.EncodeToString(sum[:]) ||
		string(identity.Data["cluster-id"]) != cluster.GetId() {
		return false, errWorkerState
	}
	expectedTier := environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING
	if worker.Tier == tierProduction {
		expectedTier = environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION
	}
	if cluster.GetRegion() != worker.Region || cluster.GetTier() != expectedTier {
		return false, errWorkerState
	}
	ready := cluster.GetIsActive() && cluster.GetLastHeartbeat() != nil &&
		time.Since(cluster.GetLastHeartbeat().AsTime()) < workerHeartbeatMaxAge &&
		cluster.GetBuildsEnabled() == worker.Builds.Allowed &&
		cluster.GetBuildsAllowed() == worker.Builds.Allowed &&
		cluster.GetApplicationsAllowed() == worker.ApplicationsAllowed
	return ready, nil
}

func (k *Kubernetes) configureEnrollment(
	ctx context.Context,
	state *corev1.ConfigMap,
	created *clusterv1.CreateClusterResponse,
) error {
	key, err := k.Core.Secrets(k.Target.Namespace).Get(ctx, workerRelease+"-agent-key", metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return safeKubernetesError("inspect worker identity", err)
	}
	if created.GetJoinToken() == nil {
		if apierrors.IsNotFound(err) || len(key.Data["private-key"]) != ed25519.PrivateKeySize {
			return errRecoveryRequired
		}
		if string(key.Data["cluster-id"]) != created.GetClusterId() {
			return errWorkerState
		}
	} else {
		// Pending registration may reissue its one-time token after a lost response.
		// Replacing this unconsumed credential does not rotate an agent identity.
		secret := &corev1.Secret{
			Name:        "loco-worker-enrollment",
			Annotations: map[string]string{installationID: string(state.UID)},
			Labels:      state.Labels,
			Data:        map[string][]byte{"join-token": []byte(created.GetJoinToken().GetToken())},
		}
		previous, getErr := k.Core.Secrets(k.Target.Namespace).Get(ctx, secret.Name, metav1.GetOptions{})
		switch {
		case getErr == nil:
			if previous.Annotations[installationID] != string(state.UID) {
				return errForeignOwnership
			}
			secret.ResourceVersion = previous.ResourceVersion
			_, err = k.Core.Secrets(k.Target.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
		case apierrors.IsNotFound(getErr):
			_, err = k.Core.Secrets(k.Target.Namespace).Create(ctx, secret, metav1.CreateOptions{})
		default:
			return safeKubernetesError("read worker enrollment", getErr)
		}
		if err != nil {
			return safeKubernetesError("store worker enrollment", err)
		}
	}
	return nil
}

func (k *Kubernetes) configureRegistry(
	ctx context.Context,
	state *corev1.ConfigMap,
	registry Registry,
	inputs map[string][]byte,
) error {
	for _, role := range []string{"pull", "push"} {
		auth := map[string]any{
			"auths": map[string]any{
				registryHost(registry): map[string]string{
					"username":          string(inputs[role+"-username"]),
					secretPasswordField: string(inputs[role+"-password"]),
				},
			},
		}
		encoded, err := json.Marshal(auth)
		if err != nil {
			return err
		}
		secret, err := k.ensureSecret(
			ctx,
			state,
			"loco-registry-"+role,
			[]string{corev1.DockerConfigJsonKey},
			map[string][]byte{corev1.DockerConfigJsonKey: encoded},
		)
		if err != nil {
			return err
		}
		if secret.Type != corev1.SecretTypeDockerConfigJson ||
			!bytes.Equal(secret.Data[corev1.DockerConfigJsonKey], encoded) {
			return errSecretInvalid
		}
	}
	return nil
}

func recordWorkerTarget(state map[string]string, name, uid string) (string, bool, error) {
	key := "worker." + name
	recordedUID, requestID := state[key+".clusterUID"], state[key+".requestID"]
	if recordedUID != "" {
		if recordedUID != uid {
			return "", false, errTargetChanged
		}
		if _, err := uuid.Parse(requestID); err != nil {
			return "", false, errWorkerState
		}
		return requestID, false, nil
	}
	if requestID != "" {
		return "", false, errWorkerState
	}
	requestID = uuid.NewString()
	state[key+".clusterUID"] = uid
	state[key+".requestID"] = requestID
	return requestID, true, nil
}

func bindWorkerRequest(state map[string]string, requestID string) error {
	if existing := state["requestID"]; existing != "" && existing != requestID {
		return errWorkerState
	}
	state["requestID"] = requestID
	return nil
}
