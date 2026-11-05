package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

const certManagerName = "cert-manager"
const envoyGatewayName = "loco-envoy-gateway"

var errPrerequisiteNotReady = errors.New("pinned prerequisite controller is missing or not ready")

var errPrerequisiteRequired = errors.New("pinned prerequisite needs installation")
var errPrerequisiteConflict = errors.New("existing prerequisite does not match the pinned release")
var errGatewayConfiguration = fmt.Errorf(
	"existing Envoy Gateway requires GatewayNamespace mode, default watch scope and pinned controller identity: %w",
	errPrerequisiteConflict,
)

func prerequisites(ctx context.Context, kube *Kubernetes, release releaseArtifacts, install bool) error {
	gateway, err := gatewayChart(release.worker)
	if err != nil {
		return err
	}
	controllerName, err := gatewayControllerName(gateway)
	if err != nil {
		return err
	}
	gatewayValues, err := gatewayPrerequisiteValues(release.worker, controllerName)
	if err != nil {
		return err
	}
	for _, prerequisite := range []struct {
		name, namespace string
		chart           []byte
		values          []byte
	}{
		{
			certManagerName, certManagerName,
			release.certManager, []byte("crds:\n  enabled: true\n"),
		},
		{
			envoyGatewayName, kube.Target.Namespace,
			gateway, gatewayValues,
		},
	} {
		files, err := archiveFiles(prerequisite.chart)
		if err != nil {
			return err
		}
		var metadata struct {
			Name       string `json:"name"`
			AppVersion string `json:"appVersion"`
		}
		for path, data := range files {
			if strings.Count(path, "/") == 1 && strings.HasSuffix(path, "/Chart.yaml") {
				if err = yaml.Unmarshal(data, &metadata); err != nil {
					return errInvalidRelease
				}
			}
		}
		if !label(metadata.Name) {
			return errInvalidRelease
		}
		selector := "app.kubernetes.io/name=" + metadata.Name
		existing, err := kube.Apps.Deployments("").List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return safeKubernetesError("inspect prerequisite controller", err)
		}
		if len(existing.Items) > 0 {
			for _, deployment := range existing.Items {
				if deployment.Labels["app.kubernetes.io/version"] != metadata.AppVersion {
					return fmt.Errorf("%s: %w", prerequisite.name, errPrerequisiteConflict)
				}
				if prerequisite.name == envoyGatewayName {
					if err := kube.requireGatewayNamespace(ctx, deployment, controllerName); err != nil {
						return err
					}
				}
				components := []string{deployment.Name}
				if prerequisite.name == certManagerName {
					components = append(components, deployment.Name+"-webhook", deployment.Name+"-cainjector")
				}
				for _, component := range components {
					if readyErr := kube.prerequisiteReady(
						ctx,
						deployment.Namespace,
						component,
						metadata.AppVersion,
						install,
					); readyErr != nil {
						return readyErr
					}
				}
			}
			continue
		}
		if !install {
			return fmt.Errorf("%s: %w", prerequisite.name, errPrerequisiteRequired)
		}
		prerequisiteTarget := kube.Target
		prerequisiteTarget.Namespace = prerequisite.namespace
		if err := command(
			ctx,
			"create prerequisite namespace",
			[]byte(fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", prerequisite.namespace)),
			"kubectl",
			"--context",
			kube.Target.Context,
			"apply",
			"-f",
			"-",
		); err != nil {
			return err
		}
		if err := installChart(
			ctx,
			prerequisiteTarget,
			prerequisite.name,
			prerequisite.chart,
			prerequisite.values,
		); err != nil {
			return err
		}
	}
	return nil
}

func gatewayControllerName(chart []byte) (string, error) {
	files, err := archiveFiles(chart)
	if err != nil {
		return "", err
	}
	var values map[string]any
	if yaml.Unmarshal(files["gateway-helm/values.yaml"], &values) != nil {
		return "", errInvalidRelease
	}
	name, found, err := unstructured.NestedString(values, "config", "envoyGateway", "gateway", "controllerName")
	if err != nil || !found || name == "" {
		return "", errInvalidRelease
	}
	return name, nil
}

func gatewayPrerequisiteValues(worker []byte, controllerName string) ([]byte, error) {
	files, err := archiveFiles(worker)
	if err != nil {
		return nil, err
	}
	var values struct {
		Gateway map[string]any `json:"gateway-helm"`
	}
	if yaml.Unmarshal(files["loco-worker/values.yaml"], &values) != nil {
		return nil, errInvalidRelease
	}
	configuration, found, err := unstructured.NestedMap(values.Gateway, "config", "envoyGateway")
	if err != nil || !found || !gatewayNamespaceConfiguration(configuration) {
		return nil, errInvalidRelease
	}
	name, found, err := unstructured.NestedString(configuration, "gateway", "controllerName")
	if err != nil || (found && name != controllerName) {
		return nil, errInvalidRelease
	}
	crds, found, err := unstructured.NestedBool(values.Gateway, "crds", "enabled")
	if err != nil || !found || crds {
		return nil, errInvalidRelease
	}
	delete(values.Gateway, "enabled")
	return yaml.Marshal(values.Gateway)
}

func (kube *Kubernetes) requireGatewayNamespace(
	ctx context.Context, deployment appsv1.Deployment, controllerName string,
) error {
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name != "envoy-gateway-config" || volume.ConfigMap == nil {
			continue
		}
		configuration, err := kube.Core.ConfigMaps(deployment.Namespace).
			Get(ctx, volume.ConfigMap.Name, metav1.GetOptions{})
		if err != nil {
			return safeKubernetesError("inspect Envoy Gateway configuration", err)
		}
		var gateway map[string]any
		if yaml.Unmarshal([]byte(configuration.Data["envoy-gateway.yaml"]), &gateway) != nil {
			return errGatewayConfiguration
		}
		name, found, err := unstructured.NestedString(gateway, "gateway", "controllerName")
		if err != nil || !found || name != controllerName || !gatewayNamespaceConfiguration(gateway) {
			return errGatewayConfiguration
		}
		return nil
	}
	return errGatewayConfiguration
}

func gatewayNamespaceConfiguration(configuration map[string]any) bool {
	_, watch, err := unstructured.NestedFieldNoCopy(configuration, "provider", "kubernetes", "watch")
	if err != nil || watch {
		return false
	}
	mode, found, err := unstructured.NestedString(configuration, "provider", "kubernetes", "deploy", "type")
	return err == nil && found && mode == "GatewayNamespace"
}

func gatewayChart(worker []byte) ([]byte, error) {
	files, err := archiveFiles(worker)
	if err != nil {
		return nil, err
	}
	for name, data := range files {
		if strings.Contains(name, "/charts/gateway-helm-") && strings.HasSuffix(name, ".tgz") {
			return data, nil
		}
	}
	selected := map[string][]byte{}
	for name, data := range files {
		_, tail, ok := strings.Cut(name, "/charts/gateway-helm/")
		if ok {
			selected["gateway-helm/"+tail] = data
		}
	}
	if len(selected) == 0 {
		return nil, errInvalidRelease
	}
	return packageArchive(selected)
}

func packageArchive(files map[string][]byte) ([]byte, error) {
	var result bytes.Buffer
	compressed := gzip.NewWriter(&result)
	writer := tar.NewWriter(compressed)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := files[name]
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data))}); err != nil {
			return nil, err
		}
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

const prerequisitePollInterval = 2 * time.Second

func (k *Kubernetes) prerequisiteReady(ctx context.Context, namespace, name, version string, wait bool) error {
	timer := time.NewTicker(prerequisitePollInterval)
	defer timer.Stop()
	for {
		deployment, err := k.Apps.Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return safeKubernetesError("read prerequisite readiness", err)
		}
		if err == nil {
			if deployment.Labels["app.kubernetes.io/version"] != version {
				return errPrerequisiteConflict
			}
			desired := int32(1)
			if deployment.Spec.Replicas != nil {
				desired = *deployment.Spec.Replicas
			}
			if desired > 0 && deployment.Status.ObservedGeneration >= deployment.Generation &&
				deployment.Status.ReadyReplicas >= desired &&
				deployment.Status.AvailableReplicas >= desired &&
				deployment.Status.UpdatedReplicas >= desired {
				return nil
			}
		}
		if !wait {
			return errPrerequisiteNotReady
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
