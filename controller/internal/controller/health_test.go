package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const testMinReadySeconds = int32(10)

func TestDesiredDeploymentProbesReadiness(t *testing.T) {
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
			deployment, err := desiredDeployment(
				app,
				testWorkspaceNamespace(app),
				envSecretMount{},
				testTelemetry(),
				testMinReadySeconds,
			)
			if err != nil {
				t.Fatal(err)
			}
			pod := deployment.Spec.Template.Spec
			if pod.RestartPolicy == nil || *pod.RestartPolicy != corev1.RestartPolicyAlways {
				t.Fatal("pod must restart exited processes")
			}
			container := pod.Containers[0]
			if tc.health == nil {
				if container.LivenessProbe != nil {
					t.Errorf("omitted health produced liveness probe %+v", container.LivenessProbe)
				}
				readiness := container.ReadinessProbe
				if readiness == nil || readiness.TCPSocket == nil || readiness.HTTPGet != nil ||
					readiness.TCPSocket.Port.IntVal != app.Spec.ServiceSpec.Deployment.Port {
					t.Errorf("readiness probe = %+v, want a TCP check on the service port", readiness)
				}
				return
			}
			for _, probe := range []*corev1ac.ProbeApplyConfiguration{container.LivenessProbe, container.ReadinessProbe} {
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

func TestDesiredDeploymentKeepsOldReplicasUntilNewOnesStayReady(t *testing.T) {
	app := testApplication()
	deployment, err := desiredDeployment(app, testWorkspaceNamespace(app), envSecretMount{}, testTelemetry(), 17)
	if err != nil {
		t.Fatal(err)
	}
	spec := deployment.Spec
	if spec.MinReadySeconds == nil || *spec.MinReadySeconds != 17 {
		t.Errorf("minReadySeconds = %v, want 17", spec.MinReadySeconds)
	}
	rolling := spec.Strategy.RollingUpdate
	if rolling.MaxUnavailable == nil || rolling.MaxUnavailable.IntValue() != 0 ||
		rolling.MaxUnavailable.Type != intstr.Int {
		t.Errorf("maxUnavailable = %v, want 0", rolling.MaxUnavailable)
	}
	if rolling.MaxSurge == nil || rolling.MaxSurge.String() != "25%" {
		t.Errorf("maxSurge = %v, want 25%%", rolling.MaxSurge)
	}
}
