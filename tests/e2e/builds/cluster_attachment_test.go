package builds

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const agentExitTimeout = 2 * time.Minute

// TestClusterAttachment enrolls, reconnects, re-enrolls, pauses, removes and restores the run's
// cluster. It restarts the API and the agent, so it runs before the parallel tests.
func TestClusterAttachment(t *testing.T) {
	w := p.NewTenant(t)
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, locofile.File{Partial: "attached", Services: map[string]locofile.Service{
		"attached": platform.ImageService(p.PublicImage),
	}})
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")

	t.Run("agent restart without the join token keeps the cluster", func(t *testing.T) {
		if err := p.RestartAgent(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := clusterIDByKey(t); got != p.ClusterID {
			t.Errorf("cluster ID after reconnect = %s, want %s", got, p.ClusterID)
		}
		requireOneCluster(t)
	})

	t.Run("only a system admin manages clusters", func(t *testing.T) {
		_, err := p.User.ClusterAdmin.ListClusters(t.Context(), &clusterv1.ListClustersRequest{})
		if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
			t.Errorf("ListClusters as a workspace user: code %v (%v), want permission_denied", code, err)
		}
		_, err = p.Clients(
			p.JoinToken,
		).ClusterAdmin.PauseCluster(
			t.Context(),
			&clusterv1.PauseClusterRequest{ClusterId: p.ClusterID},
		)
		if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
			t.Errorf("PauseCluster with the join token: code %v (%v), want unauthenticated", code, err)
		}
		if out := locoadm(t, "cluster", "list"); !strings.Contains(out, p.ClusterID) {
			t.Errorf("locoadm cluster list does not list %s:\n%s", p.ClusterID, out)
		}
	})

	t.Run("a join token enrolls once", func(t *testing.T) {
		if code := enroll(t, p.JoinToken); code != connect.CodeUnauthenticated {
			t.Errorf("second enrollment with the join token: code %v, want unauthenticated", code)
		}
		requireOneCluster(t)
	})

	t.Run("a restarted API keeps the configured admin", func(t *testing.T) {
		p.StopAPI()
		if err := p.StartAPI(t.Context()); err != nil {
			t.Fatal(err)
		}
		if out := locoadm(t, "cluster", "list"); !strings.Contains(out, p.ClusterID) {
			t.Errorf("locoadm cluster list after an API restart does not list %s:\n%s", p.ClusterID, out)
		}
	})

	t.Run("the agent reconnects across API processes", reconnectAcrossAPIProcesses)

	t.Run("re-enrollment rotates the agent key", reenrollmentRotatesKey)

	t.Run("pause, remove and restore", pauseRemoveRestore)
}

func locoadm(t *testing.T, args ...string) string {
	t.Helper()
	out, err := p.Locoadm(t.Context(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func clusterIDByKey(t *testing.T) string {
	t.Helper()
	var id string
	if err := p.DB.QueryRow(t.Context(), "SELECT id FROM clusters WHERE cluster_key = $1", p.ClusterKey).
		Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func requireOneCluster(t *testing.T) {
	t.Helper()
	var clusters int
	if err := p.DB.QueryRow(t.Context(), "SELECT count(*) FROM clusters").Scan(&clusters); err != nil {
		t.Fatal(err)
	}
	if clusters != 1 {
		t.Errorf("the registry holds %d clusters, want 1", clusters)
	}
}

func clusterActive(t *testing.T) bool {
	t.Helper()
	var active bool
	if err := p.DB.QueryRow(t.Context(), "SELECT is_active FROM clusters WHERE id = $1", p.ClusterID).
		Scan(&active); err != nil {
		t.Fatal(err)
	}
	return active
}

// enroll registers the cluster's key with joinToken and a new public key, and returns the code
// the API answers with.
func enroll(t *testing.T, joinToken string) connect.Code {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	_, err := p.Clients("").Agent.Register(t.Context(), &agentv1.RegisterRequest{
		ClusterKey:   p.ClusterKey,
		AgentVersion: p.Version,
		Role:         clusterv1.ClusterRole_CLUSTER_ROLE_ALL,
		Enrollment:   &agentv1.Enrollment{JoinToken: joinToken, PublicKey: key},
	})
	return connect.CodeOf(err)
}

func joinToken(t *testing.T) string {
	t.Helper()
	var issued struct {
		JoinToken struct {
			Token string `json:"token"`
		} `json:"joinToken"`
	}
	if err := json.Unmarshal([]byte(locoadm(t, "cluster", "join-token", p.ClusterID)), &issued); err != nil {
		t.Fatal(err)
	}
	return issued.JoinToken.Token
}

func agentKeyFingerprint(t *testing.T) string {
	t.Helper()
	var listed struct {
		Clusters []struct {
			ID                  string `json:"id"`
			AgentKeyFingerprint string `json:"agentKeyFingerprint"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(locoadm(t, "cluster", "list")), &listed); err != nil {
		t.Fatal(err)
	}
	for _, cluster := range listed.Clusters {
		if cluster.ID == p.ClusterID {
			return cluster.AgentKeyFingerprint
		}
	}
	t.Fatalf("locoadm cluster list does not list %s", p.ClusterID)
	return ""
}

func agentErrors(t *testing.T) []string {
	t.Helper()
	lines, err := p.LogLines("agent", `"level":"ERROR"`)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func reconnectAcrossAPIProcesses(t *testing.T) {
	replica, err := p.StartAPIReplica(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p.StopAgent()
	if err := p.StartAgent(t.Context(), replica, ""); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := p.DB.QueryRow(t.Context(), "SELECT sync_generation FROM clusters WHERE id = $1", p.ClusterID).
		Scan(&generation); err != nil {
		t.Fatal(err)
	}
	p.StopAPIReplica()
	if _, err := p.StartAPIReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.WaitAgentRegistered(t.Context(), generation); err != nil {
		t.Fatalf("agent reconnect after an API restart: %v", err)
	}
	p.StopAgent()
	p.StopAPIReplica()
	if err := p.StartAgent(t.Context(), p.APIURL, ""); err != nil {
		t.Fatal(err)
	}
	requireOneCluster(t)
}

func pauseRemoveRestore(t *testing.T) {
	unused := joinToken(t)
	errorsBefore := agentErrors(t)
	locoadm(t, "cluster", "pause", p.ClusterID)
	if clusterActive(t) {
		t.Error("pause left the cluster active for assignment")
	}
	if out, err := p.Locoadm(t.Context(), "cluster", "remove", p.ClusterID); err == nil {
		t.Errorf("removal without --force accepted a cluster with workloads:\n%s", out)
	}
	if out := locoadm(t, "cluster", "remove", p.ClusterID, "--force"); !strings.Contains(out, "placement") {
		t.Errorf("forced removal does not report the workloads:\n%s", out)
	}
	platform.Eventually(
		t,
		agentExitTimeout,
		"the agent to exit on the removed cluster",
		func(context.Context) error {
			if exited, _ := p.AgentExited(); !exited {
				return errors.New("the agent still runs")
			}
			return nil
		},
	)
	if _, code := p.AgentExited(); code != 1 {
		t.Errorf("agent exit code on the removed cluster = %d, want 1", code)
	}
	removal := agentErrors(t)[len(errorsBefore):]
	if len(removal) != 1 || !strings.Contains(removal[0], `"msg":"agent error"`) ||
		!strings.Contains(removal[0], "permission_denied: cluster is removed") {
		t.Errorf("agent ERROR entries on removal = %q, want one permission_denied: cluster is removed", removal)
	}
	p.ExpectAgentErrors(removal...)
	if code := enroll(t, unused); code != connect.CodeUnauthenticated {
		t.Errorf("enrollment of the removed cluster: code %v, want unauthenticated", code)
	}
	if out, err := p.Locoadm(t.Context(), "cluster", "join-token", p.ClusterID); err == nil {
		t.Errorf("a removed cluster got a join token:\n%s", out)
	}
	if out, err := p.Locoadm(t.Context(), "cluster", "resume", p.ClusterID); err == nil {
		t.Errorf("a removed cluster resumed without a restore:\n%s", out)
	}
	locoadm(t, "cluster", "restore", p.ClusterID)
	if clusterActive(t) {
		t.Error("restore made the cluster active")
	}
	p.StopAgent()
	if err := p.StartAgent(t.Context(), p.APIURL, ""); err != nil {
		t.Fatal(err)
	}
	locoadm(t, "cluster", "resume", p.ClusterID)
	platform.Eventually(t, 2*time.Minute, "the restored cluster's heartbeat", func(ctx context.Context) error {
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
	requireOneCluster(t)
}

func reenrollmentRotatesKey(t *testing.T) {
	previous := agentKeyFingerprint(t)
	token := joinToken(t)
	p.StopAgent()
	if err := p.DeleteAgentKey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.StartAgent(t.Context(), p.APIURL, token); err != nil {
		t.Fatal(err)
	}
	if got := clusterIDByKey(t); got != p.ClusterID {
		t.Errorf("cluster ID after re-enrollment = %s, want %s", got, p.ClusterID)
	}
	if agentKeyFingerprint(t) == previous {
		t.Error("re-enrollment kept the agent key")
	}
}
