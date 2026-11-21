package controller

import (
	"fmt"
	"strconv"
	"strings"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// kubeletHTTPStatus precedes the response code in the kubelet's note for an HTTP probe that got an error status.
const kubeletHTTPStatus = "HTTP probe failed with statuscode: "

// healthCheckDetail names the configured health check and a cause; the kubelet's note never reaches the user.
func healthCheckDetail(deployment *locov1alpha1.ServiceDeploymentSpec, note string) string {
	if deployment == nil || deployment.Port == nil {
		return ""
	}
	check := fmt.Sprintf("TCP :%d", *deployment.Port)
	var timeout int32
	if health := deployment.HealthCheck; health != nil {
		check = fmt.Sprintf("GET :%d%s", *deployment.Port, health.Path)
		timeout = health.Timeout
	}
	return "Health check " + check + " " + healthCheckCause(note, timeout)
}

// healthCheckCause classifies a kubelet probe failure note into one of a few fixed phrases.
func healthCheckCause(note string, timeout int32) string {
	if _, status, found := strings.Cut(note, kubeletHTTPStatus); found {
		if code, err := strconv.Atoi(strings.TrimSpace(status)); err == nil {
			return fmt.Sprintf("returned HTTP %d", code)
		}
	}
	switch {
	case strings.Contains(note, "connection refused"):
		return "failed: connection refused"
	case strings.Contains(note, "deadline exceeded"), strings.Contains(note, "timeout"),
		strings.Contains(note, "timed out"):
		if timeout > 0 {
			return fmt.Sprintf("timed out after %ds", timeout)
		}
		return "timed out"
	case strings.Contains(note, "connection reset"), strings.Contains(note, "broken pipe"),
		strings.HasSuffix(note, "EOF"):
		return "failed: connection closed"
	default:
		return "failed"
	}
}
