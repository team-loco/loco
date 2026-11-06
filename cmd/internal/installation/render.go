package installation

import "context"

func renderInstallation(ctx context.Context, config Config, release releaseArtifacts) error {
	for _, complete := range []bool{false, true} {
		values, err := controlPlaneValues(config, release.manifest, complete)
		if err != nil {
			return err
		}
		if err := renderChart(
			ctx,
			config.ControlPlane.Kubernetes,
			controlPlaneRelease,
			release.controlPlane,
			values,
		); err != nil {
			return err
		}
	}
	gateway, err := gatewayChart(release.worker)
	if err != nil {
		return err
	}
	for _, target := range append([]KubernetesTarget{config.ControlPlane.Kubernetes}, workerTargets(config.Workers)...) {
		certTarget := target
		certTarget.Namespace = certManagerName
		if err := renderChart(
			ctx,
			certTarget,
			certManagerName,
			release.certManager,
			[]byte("crds:\n  enabled: true\n"),
		); err != nil {
			return err
		}
		if err := renderChart(
			ctx,
			target,
			"loco-envoy-gateway",
			gateway,
			[]byte("crds:\n  enabled: false\n"),
		); err != nil {
			return err
		}
	}
	for _, worker := range config.Workers {
		values, err := workerValues(config, worker, release.manifest)
		if err != nil {
			return err
		}
		if err := renderChart(ctx, worker.Kubernetes, workerRelease, release.worker, values); err != nil {
			return err
		}
	}
	return nil
}

func workerTargets(workers []Worker) []KubernetesTarget {
	targets := make([]KubernetesTarget, len(workers))
	for i, worker := range workers {
		targets[i] = worker.Kubernetes
	}
	return targets
}
