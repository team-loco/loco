package controlplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

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
	cp.checkNodeLogs(t, reader, readerPassword, pod)
	cp.checkPlatformCollection(t, reader, readerPassword, pod)
}

func (cp *controlPlane) checkNodeLogs(t *testing.T, reader, password, clickhousePod string) {
	t.Helper()
	var collectors appsv1.DaemonSetList
	c.MustGet(t, &collectors, "daemonsets", "-A", "-l",
		"app.kubernetes.io/component=agent-collector,app.kubernetes.io/instance="+release)
	if len(collectors.Items) != 1 {
		t.Fatalf("found %d control-plane node collectors, want the chart's one DaemonSet", len(collectors.Items))
	}
	collector := collectors.Items[0]
	nodeNamespace := collector.Namespace
	if nodeNamespace == cp.namespace {
		t.Fatal("node log access shares the control-plane namespace")
	}
	if collector.Annotations["meta.helm.sh/release-name"] != release ||
		collector.Annotations["meta.helm.sh/release-namespace"] != cp.namespace {
		t.Fatal("the node collector is not owned by the installed control-plane release")
	}
	var namespace corev1.Namespace
	c.MustGet(t, &namespace, "namespace", nodeNamespace)
	if namespace.Labels["pod-security.kubernetes.io/enforce"] != "privileged" {
		t.Fatal("the node collector namespace does not permit its host log mounts")
	}
	var apiToken, collectorToken corev1.Secret
	tokenName := release + "-system-ingest-token"
	c.MustGet(t, &apiToken, "-n", cp.namespace, "secret", tokenName)
	c.MustGet(t, &collectorToken, "-n", nodeNamespace, "secret", tokenName)
	if len(apiToken.Data["token"]) == 0 || !bytes.Equal(apiToken.Data["token"], collectorToken.Data["token"]) {
		t.Fatal("the node collector does not have the API's system ingest credential")
	}
	rollout(t, nodeNamespace, "daemonset/"+collector.Name)
	// Restart the producers after collection is ready; checkpoints may already cover
	// their earlier output when this suite reuses a kept cluster.
	emittedAfter := time.Now().UnixNano()
	for _, workload := range []string{release + "-api", release + "-obs-proxy-ingest"} {
		c.MustKubectl(t, "-n", cp.namespace, "rollout", "restart", "deployment/"+workload)
		rollout(t, cp.namespace, "deployment/"+workload)
	}
	for _, component := range []string{"loco-api", "loco-obs-proxy"} {
		var receipt string
		platform.Eventually(
			t,
			metricsTimeout,
			component+" stdout reaches storage through the node collector",
			func(ctx context.Context) error {
				row, err := c.Kubectl(
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
						"SELECT ServiceName, LogAttributes['version'] AS version, ResourceAttributes['k8s.node.name'] AS node, "+
							"LogAttributes['log.file.path'] AS file, ResourceAttributes['loco.region'] AS region "+
							"FROM loco_obs.otel_logs WHERE WorkspaceId = '%s' AND ServiceName = '%s' "+
							"AND ResourceAttributes['k8s.node.name'] != '' AND ResourceAttributes['loco.telemetry.scope'] = 'platform' "+
							"AND LogAttributes['log.file.path'] != '' AND LogAttributes['version'] != '' "+
							"AND ResourceAttributes['loco.region'] != '' "+
							"AND Timestamp >= fromUnixTimestamp64Nano(%d) LIMIT 1 FORMAT JSONEachRow",
						systemtenant.WorkspaceID,
						component,
						emittedAfter,
					),
				)
				if err != nil {
					return err
				}
				receipt = strings.TrimSpace(row)
				if !json.Valid([]byte(receipt)) {
					return fmt.Errorf("no node-collected %s logs", component)
				}
				return nil
			},
		)
		t.Logf("synthetic installation: restarted %s; stored node-collected startup log: %s", component, receipt)
	}
}
