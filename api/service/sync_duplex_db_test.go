package service

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/api/pkg/pgnotify"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

type syncBacklogBucket struct {
	*fakeBucket
	signature string
}

func (b syncBacklogBucket) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://bucket.test/" + key + "?signature=" + b.signature, nil
}

func TestSyncAdmitsReportsWhileBuildCommandsFillTheResponseWindow(t *testing.T) {
	f := newBuildFixture(t)
	const builds = 500
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO builds (method, platform, id, resource_id, cluster_id, status, source_type, source_key,
                    source_size, dockerfile_path, image_repository, created_by)
SELECT 'cli', 'linux/amd64', gen_random_uuid(), $1, $2, 'queued', 'upload', 'duplex-' || n,
       10, 'Dockerfile', 'registry.loco.test/x', $3
FROM generate_series(1, $4::int) n`, f.resourceID, f.clusterID, f.userID, builds); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 600)
	//nolint:gosec // Synthetic source URL entropy keeps response compression from hiding flow control.
	random := rand.New(rand.NewPCG(1, 2))
	for i := range payload {
		payload[i] = byte(random.Uint32())
	}
	bucket := syncBacklogBucket{fakeBucket: f.bucket, signature: base64.RawURLEncoding.EncodeToString(payload)}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	notifier := pgnotify.New(f.pool.Config().ConnConfig, "placements", 200*time.Millisecond)
	if err := notifier.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	go notifier.Run(ctx)
	handler := NewAgentServer(f.pool, f.queries, notifier, bucket, nil, testHeartbeatInterval, testAgentConfig())
	rpc := connect.NewServer()
	agentv1connect.RegisterAgentServiceHandler(rpc, handler)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	mux.HandleFunc("GET /test/build-failures", func(w http.ResponseWriter, r *http.Request) {
		var failed int
		const countFailed = `SELECT count(*) FROM builds WHERE cluster_id=$1 AND status='failed'`
		if err := f.pool.QueryRow(r.Context(), countFailed, f.clusterID).Scan(&failed); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if failed != builds {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.Config.HTTP2 = &http.HTTP2Config{MaxReceiveBufferPerStream: 16 << 10}
	srv.StartTLS()
	defer func() { srv.CloseClientConnections(); srv.Close() }()
	agentDir, err := filepath.Abs("../../agent")
	if err != nil {
		t.Fatal(err)
	}
	agentBinary := filepath.Join(t.TempDir(), "agent.test")
	build := exec.CommandContext(ctx, "go", "test", "-race", "-mod=readonly", "-c", "-o", agentBinary, ".")
	build.Dir = agentDir
	if buildOutput, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build agent sync fixture: %v\n%s", buildErr, buildOutput)
	}
	// Compile before issuing the assertion: a cold build can exceed its one-minute lifetime.
	cmd := exec.CommandContext(ctx, agentBinary, "-test.run=^TestSyncAgainstRealAPIBacklog$", "-test.v")
	cmd.Dir = agentDir
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	cmd.Env = append(os.Environ(), "LOCO_TEST_SYNC_URL="+srv.URL, "LOCO_TEST_SYNC_CERTIFICATE="+string(certificate),
		"LOCO_TEST_SYNC_AUTH="+testAssertion(t, f.clusterID))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("agent sync fixture: %v\n%s", err, output)
	}
	t.Logf(
		"actual API and agent completed 1,000-placement inventory and recorded %d permanent build failures",
		builds,
	)
	t.Logf("agent fixture:\n%s", output)
	var failed int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM builds WHERE cluster_id=$1 AND status='failed'`, f.clusterID).
		Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != builds {
		t.Fatalf("stored %d failed builds, want %d", failed, builds)
	}
}
