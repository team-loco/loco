package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestDesiredDeploymentHealthProbesAreOptional(t *testing.T) {
	health := &locov1alpha1.HealthCheckSpec{
		Path:               "/healthz",
		Interval:           10,
		Timeout:            2,
		FailThreshold:      3,
		StartupGracePeriod: 5,
	}
	for _, tc := range []struct {
		name   string
		health *locov1alpha1.HealthCheckSpec
	}{
		{name: "process only"},
		{name: "http", health: health},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApplication()
			app.Spec.ServiceSpec.Deployment.HealthCheck = tc.health
			deployment, err := desiredDeployment(app, envSecretMount{}, testTelemetry())
			if err != nil {
				t.Fatal(err)
			}
			pod := deployment.Spec.Template.Spec
			if pod.RestartPolicy == nil || *pod.RestartPolicy != corev1.RestartPolicyAlways {
				t.Fatal("pod must restart exited processes")
			}
			container := pod.Containers[0]
			for _, probe := range []*corev1ac.ProbeApplyConfiguration{container.LivenessProbe, container.ReadinessProbe} {
				if tc.health == nil {
					if probe != nil {
						t.Errorf("omitted health produced probe %+v", probe)
					}
					continue
				}
				if probe == nil || probe.HTTPGet == nil ||
					*probe.HTTPGet.Path != health.Path ||
					probe.HTTPGet.Port.IntVal != app.Spec.ServiceSpec.Deployment.Port ||
					*probe.PeriodSeconds != health.Interval ||
					*probe.TimeoutSeconds != health.Timeout ||
					*probe.FailureThreshold != health.FailThreshold ||
					*probe.InitialDelaySeconds != health.StartupGracePeriod {
					t.Errorf("probe = %+v, want configured HTTP check", probe)
				}
			}
		})
	}
}
