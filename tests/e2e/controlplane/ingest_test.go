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

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"

	"github.com/team-loco/loco/internal/clusterassertion"
	"github.com/team-loco/loco/internal/systemtenant"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	assertionLifetime = time.Minute
	ingestTimeout     = 30 * time.Second
	protobufContent   = "application/x-protobuf"
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
	name := "ingest-e2e-" + platform.RandomHex(4)
	ids := strings.Fields(cp.psql(t, fmt.Sprintf(`
		WITH u AS (INSERT INTO users (email) VALUES ('%[1]s@test.local') RETURNING id),
		o AS (INSERT INTO organizations (name, created_by) SELECT '%[1]s', id FROM u RETURNING id),
		w AS (INSERT INTO workspaces (org_id, name, created_by) SELECT o.id, '%[1]s', u.id FROM o, u RETURNING id),
		c AS (INSERT INTO clusters (name, region, provider, is_active, is_default, agent_public_key)
		      VALUES ('%[1]s', 'local-1', 'kind', true, false, decode('%[2]s', 'hex')) RETURNING id),
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

	marker := "control-plane-ingest-" + platform.RandomHex(4)
	export := string(platform.OTLPLogs(t, workspace, marker))
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
	readerPassword := ""
	for _, user := range cp.values.ClickHouse.ClickHouse.Users {
		for _, grant := range user.Grants {
			if selectGrant.MatchString(grant) {
				reader = user.Name
				var secret struct {
					Data map[string][]byte `json:"data"`
				}
				c.MustGet(t, &secret, "-n", cp.namespace, "secret", user.PasswordSecretName)
				readerPassword = string(secret.Data[passwordKey])
			}
		}
	}
	traceID := platform.RandomHex(16)
	cp.request(t, http.MethodPost, api, "/loco.config.v1.ConfigService/GetConfig", "{}", http.Header{
		"Traceparent": {"00-" + traceID + "-" + platform.RandomHex(8) + "-01"},
	}).expect(t, http.StatusOK)

	pod := c.MustKubectl(t, "-n", cp.namespace, "get", "pods", "-l", "clickhouse.altinity.com/chi",
		"-o", "jsonpath={.items[0].metadata.name}")
	platform.Eventually(t, ingestTimeout, "the log row in ClickHouse", func(ctx context.Context) error {
		count, err := c.Kubectl(ctx, "-n", cp.namespace, "exec", pod, "-c", "clickhouse", "--", "clickhouse-client",
			"--user", reader, "--password", readerPassword,
			"-q", "SELECT count() FROM loco_obs.otel_logs WHERE Body = '"+marker+"'")
		if err == nil && strings.TrimSpace(count) != "1" {
			return fmt.Errorf("%s rows, want 1", strings.TrimSpace(count))
		}
		return err
	})
	platform.Eventually(
		t,
		ingestTimeout,
		"the API trace linked to an Envoy span in the system tenant",
		func(ctx context.Context) error {
			count, err := c.Kubectl(ctx, "-n", cp.namespace, "exec", pod, "-c", "clickhouse", "--", "clickhouse-client",
				"--user", reader, "--password", readerPassword,
				"-q", fmt.Sprintf(`SELECT count() FROM
				(SELECT WorkspaceId, arrayJoin(arrayZip("Links.TraceId", "Links.SpanId")) AS link
				 FROM loco_obs.otel_traces WHERE ServiceName = 'loco-api') AS api
				INNER JOIN loco_obs.otel_traces AS gateway
				ON api.link.1 = gateway.TraceId AND api.link.2 = gateway.SpanId
				WHERE gateway.TraceId = '%s' AND gateway.ServiceName = 'loco-gateway'
				AND api.WorkspaceId = '%s' AND gateway.WorkspaceId = '%s'`,
					traceID, systemtenant.WorkspaceID, systemtenant.WorkspaceID))
			if err == nil && strings.TrimSpace(count) == "0" {
				return fmt.Errorf("no API trace links to an Envoy span in trace %s", traceID)
			}
			if err == nil {
				t.Logf(
					"gateway trace %s: %s API spans link to stored gateway spans in the system tenant",
					traceID,
					strings.TrimSpace(count),
				)
			}
			return err
		},
	)
	cp.checkNodeLogs(t, issued.Token, reader, readerPassword, pod)
}

func (cp *controlPlane) checkNodeLogs(t *testing.T, token, reader, password, clickhousePod string) {
	t.Helper()
	nodeNamespace := cp.namespace
	c.MustApply(t, namespace(nodeNamespace, "privileged"))
	values := writeFile(t, "node-logs-values.yaml", fmt.Sprintf(`global:
  region: %[1]s
  platformTelemetry:
    enabled: true
    platformOnly: true
    installation: node-logs-e2e
    cluster: %[2]s
    environment: test
    namespaces: []
  exporter:
    endpoint: http://%[3]s-obs-proxy-ingest.%[4]s:%[5]d
    headers:
      Authorization: %[6]q
    ingestToken:
      enabled: false
agent:
  image: {tag: unused}
controller:
  image: {tag: unused}
cluster: {role: apps}
certManager: {existingTLSSecret: unused}
gateway-helm: {enabled: false}
otel-col-deploy: {enabled: false}
otel-col-cluster: {enabled: false}
otel-col-daemon:
  namespaceOverride: %[4]s
`, metricsRegion, c.KindCluster(), release, nodeNamespace, cp.values.ObsProxy.Ingest.HTTPPort, "Bearer "+token))
	rendered := helm(t, "template", "platform-logs", c.Path("charts", "loco-worker"), "--namespace", cp.namespace,
		"--values", values)
	var objects []any
	for _, object := range renderedDocuments(t, rendered) {
		var identity struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		}
		roundTrip(t, object, &identity)
		if identity.Metadata.Labels["app.kubernetes.io/part-of"] == collectorPartOf {
			objects = append(objects, object)
		}
	}
	applyJSON(t, objects...)
	rollout(t, nodeNamespace, "daemonset/"+nodeCollector)
	// Restart the producers after collection is ready; checkpoints may already cover
	// their earlier output when this suite reuses a kept cluster.
	emittedAfter := time.Now().UTC().Format(time.DateTime)
	for _, workload := range []string{release + "-api", release + "-obs-proxy-ingest"} {
		c.MustKubectl(t, "-n", cp.namespace, "rollout", "restart", "deployment/"+workload)
		rollout(t, cp.namespace, "deployment/"+workload)
	}
	for _, component := range []string{"loco-api", "loco-obs-proxy"} {
		platform.Eventually(
			t,
			metricsTimeout,
			component+" stdout reaches storage through the node collector",
			func(ctx context.Context) error {
				count, err := c.Kubectl(
					ctx,
					"-n",
					cp.namespace,
					"exec",
					clickhousePod,
					"-c",
					"clickhouse",
					"--",
					"clickhouse-client",
					"--user",
					reader,
					"--password",
					password,
					"-q",
					fmt.Sprintf(
						"SELECT count() FROM loco_obs.otel_logs WHERE WorkspaceId = '%s' AND ServiceName = '%s' "+
							"AND ResourceAttributes['k8s.node.name'] != '' AND ResourceAttributes['loco.telemetry.scope'] = 'platform' "+
							"AND LogAttributes['log.file.path'] != '' AND Timestamp >= toDateTime('%s')",
						systemtenant.WorkspaceID,
						component,
						emittedAfter,
					),
				)
				if err == nil && strings.TrimSpace(count) == "0" {
					return fmt.Errorf("no node-collected %s logs", component)
				}
				return err
			},
		)
		t.Logf(
			"real %s pod stdout collected from node files, enriched with node identity and stored in the system tenant",
			component,
		)
	}
}
