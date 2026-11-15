package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"

	"github.com/team-loco/loco/internal/clusterassertion"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	assertionLifetime = time.Minute
	ingestTimeout     = 30 * time.Second
	protobufContent   = "application/x-protobuf"
	// workspaceAttribute matches the one observability-proxy/pkg/ingest/tenancy.go filters on.
	workspaceAttribute = "loco.workspace.id"
)

var selectGrant = regexp.MustCompile(`^GRANT SELECT `)

// checkIngest enrolls a cluster with a placed workspace, has the API issue it an ingest token
// signed with the setup Job's transit key, and sends a log through the chart Gateway and the
// ingest Deployment to the in-cluster ClickHouse.
//
//nolint:thelper // a step of TestControlPlane
func (cp *controlPlane) checkIngest(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	name := "ingest-e2e-" + randomHex(t, 4)
	ids := strings.Fields(cp.psql(t, fmt.Sprintf(`
		WITH u AS (INSERT INTO users (email) VALUES ('%[1]s@test.local') RETURNING id),
		o AS (INSERT INTO organizations (name, created_by) SELECT '%[1]s', id FROM u RETURNING id),
		w AS (INSERT INTO workspaces (org_id, name, created_by) SELECT o.id, '%[1]s', u.id FROM o, u RETURNING id),
		c AS (INSERT INTO clusters (name, region, provider, is_active, is_default, agent_public_key)
		      VALUES ('%[1]s', 'us-east-1', 'kind', true, false, decode('%[2]s', 'hex')) RETURNING id),
		p AS (INSERT INTO placement_windows (resource_id, cluster_id, workspace_id, placed_at)
		      SELECT uuidv7(), c.id, w.id, NOW() - INTERVAL '1 hour' FROM c, w)
		SELECT c.id, w.id FROM c, w`, name, hex.EncodeToString(public)), "-F", " "))
	if len(ids) != 2 {
		t.Fatalf("enrolling the ingest cluster returned %q", ids)
	}
	cluster, workspace := ids[0], ids[1]
	assertion, err := clusterassertion.Sign(key, cluster, time.Now(), assertionLifetime)
	if err != nil {
		t.Fatal(err)
	}
	api := cp.values.ControlPlaneHostname
	var issued struct {
		Token string `json:"token"`
	}
	cp.request(t, http.MethodPost, api, "/loco.ingest.v1.IngestService/IssueToken", "{}", bearer(assertion)).
		decode(t, &issued)
	var jwks struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			Algorithm string `json:"alg"`
		} `json:"keys"`
	}
	cp.request(t, http.MethodGet, api, "/.well-known/loco-ingest-jwks.json", "", nil).decode(t, &jwks)
	signed := false
	for _, key := range jwks.Keys {
		signed = signed || key.KeyID == "v1" && key.Algorithm == "EdDSA"
	}
	if issued.Token == "" || !signed {
		t.Fatalf("got the ingest token %q and the JWKS %+v, want a token and the v1 EdDSA key", issued.Token, jwks)
	}

	marker := "control-plane-ingest-" + randomHex(t, 4)
	export := exportRequest(t, workspace, marker)
	ingest := cp.values.ObsProxy.Ingest.Hostname
	protobuf := http.Header{contentTypeHeader: {protobufContent}}
	cp.request(t, http.MethodPost, ingest, "/v1/logs", export, protobuf).expect(t, http.StatusUnauthorized)
	authorized := bearer(issued.Token)
	cp.request(t, http.MethodPost, ingest, "/v1/logs", "{}", authorized).expect(t, http.StatusUnsupportedMediaType)
	authorized.Set(contentTypeHeader, protobufContent)
	response := plogotlp.NewExportResponse()
	if err := response.UnmarshalProto(cp.request(t, http.MethodPost, ingest, "/v1/logs", export, authorized).
		expect(t, http.StatusOK)); err != nil {
		t.Fatal(err)
	}
	if rejected := response.PartialSuccess().RejectedLogRecords(); rejected != 0 {
		t.Fatalf("the ingest proxy rejected %d records of the placed workspace", rejected)
	}

	reader := ""
	for _, user := range cp.values.ClickHouse.ClickHouse.Users {
		for _, grant := range user.Grants {
			if selectGrant.MatchString(grant) {
				reader = user.Name
			}
		}
	}
	pod := c.MustKubectl(t, "-n", cp.namespace, "get", "pods", "-l", "clickhouse.altinity.com/chi",
		"-o", "jsonpath={.items[0].metadata.name}")
	platform.Eventually(t, ingestTimeout, "the log row in ClickHouse", func(ctx context.Context) error {
		count, err := c.Kubectl(ctx, "-n", cp.namespace, "exec", pod, "-c", "clickhouse", "--", "clickhouse-client",
			"--user", reader, "--password", cp.password,
			"-q", "SELECT count() FROM loco_obs.otel_logs WHERE Body = '"+marker+"'")
		if err == nil && strings.TrimSpace(count) != "1" {
			return fmt.Errorf("%s rows, want 1", strings.TrimSpace(count))
		}
		return err
	})
}

// exportRequest is an OTLP/HTTP protobuf export of one log record of workspace.
func exportRequest(t *testing.T, workspace, body string) string {
	t.Helper()
	logs := plog.NewLogs()
	resource := logs.ResourceLogs().AppendEmpty()
	resource.Resource().Attributes().PutStr("service.name", "e2e-ingest")
	resource.Resource().Attributes().PutStr(workspaceAttribute, workspace)
	record := resource.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.NewTimestampFromTime(time.Now().Truncate(time.Second)))
	record.SetSeverityText("INFO")
	record.Body().SetStr(body)
	payload, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
