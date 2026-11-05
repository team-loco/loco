package installation

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var errWorkerTargetsDuplicate = errors.New("workers must target distinct Kubernetes clusters")

var errEndpointSchemes = errors.New(
	"control-plane API, dashboard and ingest URLs must use the same HTTP or HTTPS scheme",
)

func preflightTargets(ctx context.Context, config Config, release releaseArtifacts) error {
	cpConfig := config.ControlPlane
	scheme := validatedURL(cpConfig.APIURL).Scheme
	if validatedURL(cpConfig.WebURL).Scheme != scheme ||
		validatedURL(cpConfig.Observability.IngestURL).Scheme != scheme {
		return errEndpointSchemes
	}
	cp, err := NewKubernetes(config.ControlPlane.Kubernetes)
	if err != nil {
		return err
	}
	if _, err = cp.clusterUID(ctx); err != nil {
		return err
	}
	if adminErr := cp.requireClusterAdmin(ctx); adminErr != nil {
		return adminErr
	}
	checked := map[string]bool{cp.Target.Context: true}
	root, err := cp.Core.ConfigMaps(cp.Target.Namespace).Get(ctx, installationState, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return safeKubernetesError("read installation ownership", err)
	}
	if apierrors.IsNotFound(err) {
		root = nil
	}
	if err == nil {
		if root.Labels[installationOwner] != config.Name {
			return errForeignOwnership
		}
		if _, err := cp.state(ctx, config.Name); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(config.Workers))
	for _, worker := range config.Workers {
		kube, err := NewKubernetes(worker.Kubernetes)
		if err != nil {
			return err
		}
		uid, err := kube.clusterUID(ctx)
		if err != nil {
			return err
		}
		if !checked[kube.Target.Context] {
			if adminErr := kube.requireClusterAdmin(ctx); adminErr != nil {
				return adminErr
			}
			checked[kube.Target.Context] = true
		}

		if seen[uid] {
			return errWorkerTargetsDuplicate
		}
		seen[uid] = true
		if ownershipErr := kube.preflightWorkerOwnership(ctx, config.Name, worker.Name, root); ownershipErr != nil {
			return ownershipErr
		}

		if err := prerequisites(
			ctx,
			kube,
			release,
			false,
		); err != nil && !errors.Is(err, errPrerequisiteRequired) &&
			!errors.Is(err, errPrerequisiteNotReady) {
			return err
		}
	}
	return nil
}

func (k *Kubernetes) preflightWorkerOwnership(
	ctx context.Context,
	installationName, workerName string,
	root *corev1.ConfigMap,
) error {
	if policyErr := k.checkWorkerNamespace(ctx); policyErr != nil {
		return policyErr
	}
	if root != nil {
		stored := root.Data["worker."+workerName+".clusterUID"]
		if stored != "" && stored != k.Target.ClusterUID {
			return errTargetChanged
		}
	}
	state, getErr := k.Core.ConfigMaps(k.Target.Namespace).Get(ctx, installationState, metav1.GetOptions{})
	if getErr == nil {
		if state.Labels[installationOwner] != installationName {
			return errForeignOwnership
		}
		if root == nil || state.Data["controlPlaneID"] != string(root.UID) {
			return errForeignOwnership
		}
		if state.Data["clusterUID"] != k.Target.ClusterUID {
			return errTargetChanged
		}
	} else if !apierrors.IsNotFound(getErr) {
		return safeKubernetesError("read worker ownership", getErr)
	}
	return nil
}
