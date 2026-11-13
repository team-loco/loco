package installation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/rpc"
)

var (
	errControlPlaneNotReady = errors.New("control-plane deployments are not ready")
	errWorkerStatus         = errors.New("read authenticated worker status failed")
)

type Status struct {
	ReleaseVersion   string         `json:"releaseVersion"`
	ReleaseCommit    string         `json:"releaseCommit"`
	Phase            string         `json:"phase"`
	RequiredDNS      []DNSRecord    `json:"requiredDNS"`
	Workers          []WorkerStatus `json:"workers"`
	PendingRemoval   []string       `json:"pendingRemoval,omitempty"`
	RecoveryExported bool           `json:"recoveryExported"`
}

type DNSRecord struct {
	Hostname string   `json:"hostname"`
	Targets  []string `json:"targets"`
}
type WorkerStatus struct {
	Name      string `json:"name"`
	ClusterID string `json:"clusterID"`
	Ready     bool   `json:"ready"`
}

func StatusInstallation(ctx context.Context, config Config, token string) (Status, error) {
	kube, err := NewKubernetes(config.ControlPlane.Kubernetes)
	if err != nil {
		return Status{}, err
	}
	state, err := kube.Core.ConfigMaps(kube.Target.Namespace).Get(ctx, installationState, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Status{Phase: "not-installed"}, nil
	}
	if err != nil {
		return Status{}, safeKubernetesError("read installation status", err)
	}
	if _, err = kube.state(ctx, config.Name); err != nil {
		return Status{}, err
	}
	status := Status{
		Phase:            "bootstrap-required",
		RecoveryExported: state.Data["recoveryExported"] == stateTrue,
		ReleaseVersion:   state.Data["releaseVersion"],
		ReleaseCommit:    state.Data["releaseCommit"],
	}
	configured := make(map[string]bool, len(config.Workers))
	for _, worker := range config.Workers {
		configured[worker.Name] = true
	}
	for key := range state.Data {
		if strings.HasPrefix(key, "worker.") && strings.HasSuffix(key, ".clusterUID") {
			name := strings.TrimSuffix(strings.TrimPrefix(key, "worker."), ".clusterUID")
			if !configured[name] {
				status.PendingRemoval = append(status.PendingRemoval, name)
			}
		}
	}
	sort.Strings(status.PendingRemoval)

	targets, err := kube.gatewayTargets(ctx)
	if err != nil {
		return Status{}, err
	}
	for _, endpoint := range []string{
		config.ControlPlane.APIURL, config.ControlPlane.WebURL,
		config.ControlPlane.Observability.IngestURL,
	} {
		parsed := validatedURL(endpoint)
		status.RequiredDNS = append(status.RequiredDNS, DNSRecord{Hostname: parsed.Hostname(), Targets: targets})
	}
	if err = kube.controlPlaneReady(ctx); err != nil {
		if !errors.Is(err, errControlPlaneNotReady) {
			return Status{}, err
		}
		status.Phase = "installing"
		if errors.Is(validateIssuer(ctx, kube, config.ControlPlane.Certificates.IssuerRef), ErrIssuerRequired) {
			status.Phase = "issuer-required"
		}
		return status, nil
	}
	if state.Data["bootstrapComplete"] != stateTrue {
		return status, nil
	}
	status.Phase = "workers-pending"
	if token == "" {
		return status, nil
	}
	listed, err := rpc.ForHost(
		config.ControlPlane.APIURL,
		token,
	).ClusterAdmin.ListClusters(
		ctx,
		&clusterv1.ListClustersRequest{},
	)
	if err != nil {
		return Status{}, errWorkerStatus
	}
	allReady := true
	for _, worker := range config.Workers {
		workerKube, err := NewKubernetes(worker.Kubernetes)
		if err != nil {
			return Status{}, err
		}
		workerState, err := workerKube.Core.ConfigMaps(workerKube.Target.Namespace).
			Get(ctx, installationState, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			status.Workers = append(status.Workers, WorkerStatus{Name: worker.Name})
			allReady = false
			continue
		}
		if err != nil {
			return Status{}, safeKubernetesError("read worker status", err)
		}
		if _, err = workerKube.state(ctx, config.Name); err != nil {
			return Status{}, err
		}
		workerStatus := WorkerStatus{Name: worker.Name, ClusterID: workerState.Data["clusterID"]}
		for _, cluster := range listed.GetClusters() {
			if cluster.GetId() != workerStatus.ClusterID {
				continue
			}
			var readyErr error
			workerStatus.Ready, readyErr = workerKube.workerReady(ctx, cluster, worker)
			if readyErr != nil {
				return Status{}, readyErr
			}
		}
		allReady = allReady && workerStatus.Ready
		status.Workers = append(status.Workers, workerStatus)
		workerTargets, err := workerKube.gatewayTargets(ctx)
		if err != nil {
			return Status{}, err
		}
		status.RequiredDNS = append(
			status.RequiredDNS,
			DNSRecord{Hostname: "*." + worker.AppsDomain, Targets: workerTargets},
		)
	}
	if allReady {
		status.Phase = "running"
	}
	return status, nil
}

func (k *Kubernetes) gatewayTargets(ctx context.Context) ([]string, error) {
	services, err := k.Core.Services(k.Target.Namespace).
		List(ctx, metav1.ListOptions{LabelSelector: "gateway.envoyproxy.io/owning-gateway-namespace=" + k.Target.Namespace})
	if err != nil {
		return nil, safeKubernetesError("read Gateway addresses", err)
	}
	var targets []string
	for _, service := range services.Items {
		for _, ingress := range service.Status.LoadBalancer.Ingress {
			if ingress.IP != "" {
				targets = append(targets, ingress.IP)
			}
			if ingress.Hostname != "" {
				targets = append(targets, ingress.Hostname)
			}
		}
	}
	return targets, nil
}

// Config strings have passed Parse's semantic URL validation before installation.
func validatedURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(fmt.Errorf("validated installation URL: %w", err))
	}
	return parsed
}

func (k *Kubernetes) controlPlaneReady(ctx context.Context) error {
	for _, component := range []string{"api", "ui", "obs-proxy-ingest"} {
		deployment, err := k.Apps.Deployments(k.Target.Namespace).
			Get(ctx, controlPlaneRelease+"-"+component, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return errControlPlaneNotReady
		}
		if err != nil {
			return safeKubernetesError("read control-plane readiness", err)
		}
		if deployment.Spec.Replicas == nil || deployment.Status.ReadyReplicas < *deployment.Spec.Replicas {
			return errControlPlaneNotReady
		}
	}
	return nil
}
