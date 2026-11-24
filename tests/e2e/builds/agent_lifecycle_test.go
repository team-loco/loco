package builds

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// TestAgentLifecycle checks the enrolled agent's registration, heartbeat and capacity, then deploys
// an image and follows the placement from the API through the agent to a Ready Application, whose
// workspace network policy admits only the workspace's own pods.
func TestAgentLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("health and the agent's version", func(t *testing.T) {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.APIURL+"/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Errorf("API /health = %d, want 200", response.StatusCode)
		}
		var version string
		if err := p.DB.QueryRow(t.Context(), "SELECT agent_version FROM clusters WHERE id = $1", p.ClusterID).
			Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != p.Version {
			t.Errorf("agent registered version %q, want %q", version, p.Version)
		}
	})

	t.Run("heartbeat and capacity", func(t *testing.T) {
		waitHeartbeat(t, 30*time.Second, "the agent's first heartbeat")
		var health string
		var cpu, memory int64
		if err := p.DB.QueryRow(t.Context(), clusterHealth,
			p.ClusterID).
			Scan(&health, &cpu, &memory); err != nil {
			t.Fatal(err)
		}
		if health != "healthy" {
			t.Errorf("cluster health %q, want healthy", health)
		}
		if cpu <= 0 || memory <= 0 {
			t.Errorf("agent reported capacity %dm CPU and %d bytes, want both positive", cpu, memory)
		}
	})

	w := p.NewTenant(t)
	const app = "e2e-agent-app"
	dir := t.TempDir()
	platform.WriteLocoFile(
		t,
		dir,
		locofile.File{Partial: app, Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage)}},
	)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	resourceID := w.ResourceID(t, app)

	t.Run("the agent applies the placement", func(t *testing.T) {
		platform.Eventually(
			t,
			30*time.Second,
			"the agent to create the Application at placement revision 1",
			func(ctx context.Context) error {
				application, err := p.Application(ctx, resourceID, w.EnvironmentID)
				if err != nil {
					return err
				}
				if revision := application.Annotations[v1alpha1.KeyPlacementRevision]; revision != "1" {
					return fmt.Errorf("placement revision %q", revision)
				}
				return nil
			},
		)
		platform.Eventually(
			t,
			30*time.Second,
			"the control plane to record applied revision 1",
			func(ctx context.Context) error {
				var applied int64
				if err := p.DB.QueryRow(ctx, "SELECT applied_revision FROM placements WHERE resource_id = $1", resourceID).
					Scan(&applied); err != nil {
					return err
				}
				if applied != 1 {
					return fmt.Errorf("applied revision %d", applied)
				}
				return nil
			},
		)
	})

	t.Run("the app becomes ready", func(t *testing.T) {
		platform.Eventually(
			t,
			30*time.Second,
			"the agent to report the app ready at revision 1",
			func(ctx context.Context) error {
				var ready bool
				if err := p.DB.QueryRow(ctx, readyAtRevisionOne, resourceID).
					Scan(&ready); err != nil {
					return err
				}
				if !ready {
					return errors.New("placement not ready at revision 1")
				}
				return nil
			},
		)
	})

	t.Run("network isolation", func(t *testing.T) {
		outsider := "e2e-outsider-" + strings.ToLower(resourceID[len(resourceID)-8:])
		p.MustKubectl(t, "create", "namespace", outsider)
		p.DeleteOnCleanup(t, "namespace", outsider)
		// The cluster IP leaves the network policy as the only reason for the outsider's probe to fail.
		ip := strings.TrimSpace(p.MustKubectl(t, "-n", w.Namespace, "get", "service", "resource-"+resourceID,
			"-o", "jsonpath={.spec.clusterIP}"))
		target := "http://" + ip + "/"
		if !p.Reaches(t, platform.ProbePod{Namespace: w.Namespace, Name: "e2e-probe-inside"}, target) {
			t.Error("a pod in the app's workspace namespace cannot reach the app")
		}
		if p.Reaches(t, platform.ProbePod{Namespace: outsider, Name: "e2e-probe-outside"}, target) {
			t.Error("a pod in another namespace reaches the app")
		}
	})
}

// waitHeartbeat waits until the run's cluster records a heartbeat.
func waitHeartbeat(t *testing.T, timeout time.Duration, what string) {
	t.Helper()
	platform.Eventually(t, timeout, what, func(ctx context.Context) error {
		var heartbeat bool
		if err := p.DB.QueryRow(ctx, "SELECT last_heartbeat IS NOT NULL FROM clusters WHERE id = $1", p.ClusterID).
			Scan(&heartbeat); err != nil {
			return err
		}
		if !heartbeat {
			return errors.New("no heartbeat")
		}
		return nil
	})
}

const (
	readyAtRevisionOne = "SELECT ready_replicas > 0 AND observed_revision = 1 FROM placements WHERE resource_id = $1"
	clusterHealth      = "SELECT health_status, capacity_cpu_millicores, capacity_memory_bytes FROM clusters WHERE id = $1"
)
