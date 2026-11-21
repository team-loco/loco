package controller

import (
	"testing"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestHealthCheckCause(t *testing.T) {
	const prefix = "Readiness probe failed: "
	for want, notes := range map[string][]string{
		"failed: connection refused": {
			`Get "http://10.244.0.7:5678/": dial tcp 10.244.0.7:5678: connect: connection refused`,
			"dial tcp 10.244.0.7:6379: connect: connection refused",
		},
		"returned HTTP 500": {"HTTP probe failed with statuscode: 500"},
		"timed out after 1s": {
			`Get "http://10.244.0.7:5678/": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
			"dial tcp 10.244.0.7:6379: i/o timeout",
			`timeout: failed to connect service "10.244.0.7:9090" within 1s: context deadline exceeded`,
			`command "cat /tmp/healthy" timed out`,
		},
		"failed: connection closed": {
			`Get "http://10.244.0.7:5678/": read tcp 10.244.0.1:43210->10.244.0.7:5678: read: ` +
				"connection reset by peer",
			`Get "http://10.244.0.7:5678/": EOF`,
		},
		"failed": {
			`Get "http://10.244.0.7:5678/": stopped after 10 redirects`,
			`service unhealthy (responded with "NOT_SERVING")`,
			"cat: can't open '/tmp/healthy': No such file or directory",
		},
	} {
		for _, note := range notes {
			if got := healthCheckCause(prefix+note, 1); got != want {
				t.Errorf("cause of %q = %q, want %q", note, got, want)
			}
		}
	}
}

func TestHealthCheckDetail(t *testing.T) {
	refused := "Readiness probe failed: dial tcp 10.244.0.7:6379: connect: connection refused"
	timeout := "Readiness probe failed: dial tcp 10.244.0.7:6379: i/o timeout"
	http := &locov1alpha1.ServiceDeploymentSpec{Port: new(int32(8080)), HealthCheck: &locov1alpha1.HealthCheckSpec{
		Path: "/ready", Interval: 10, Timeout: 2, FailThreshold: 3,
	}}
	tcp := &locov1alpha1.ServiceDeploymentSpec{Port: new(int32(6379))}
	for _, tt := range []struct {
		deployment *locov1alpha1.ServiceDeploymentSpec
		note       string
		want       string
	}{
		{http, refused, "Health check GET :8080/ready failed: connection refused"},
		{http, timeout, "Health check GET :8080/ready timed out after 2s"},
		{tcp, refused, "Health check TCP :6379 failed: connection refused"},
		{tcp, timeout, "Health check TCP :6379 timed out"},
		{tcp, "", "Health check TCP :6379 failed"},
	} {
		if got := healthCheckDetail(tt.deployment, tt.note); got != tt.want {
			t.Errorf("detail = %q, want %q", got, tt.want)
		}
	}
}
