package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	originHeader       = "Origin"
	contentType        = "Content-Type"
	otherWorkspace     = "00000000-0000-7000-8000-999999999999"
	queryWindow        = time.Hour
	ingestTokenTimeout = time.Minute
	// workspaceAttribute matches the one observability-proxy/pkg/ingest/tenancy.go filters on.
	workspaceAttribute = "loco.workspace.id"
)

// TestObservabilityProxy checks the observability reads and the OTLP ingest proxy: health, token
// authentication, revocation and workspace scope of QueryLogs, its browser CORS answers, and OTLP
// ingest that takes only the enrolled cluster's ingest token and only workspaces placed on it.
func TestObservabilityProxy(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)

	t.Run("health", func(t *testing.T) {
		for _, path := range []string{"/healthz", "/readyz"} {
			if status := httpStatus(t, http.MethodGet, p.ObsProxyURL+path, nil, nil); status != http.StatusOK {
				t.Errorf("observability proxy %s = %d", path, status)
			}
		}
	})

	t.Run("QueryLogs needs a valid token for the workspace", func(t *testing.T) {
		for token, want := range map[string]connect.Code{
			"": connect.CodeUnauthenticated, "loco_k_not-a-token": connect.CodeUnauthenticated,
		} {
			if code := queryLogsCode(t, token, w.ID); code != want {
				t.Errorf("QueryLogs with token %q: code %v, want %v", token, code, want)
			}
		}
		revoked := w.MintToken(t, "e2e-proxy-revoked", tokenv1.Scope_SCOPE_READ)
		if _, err := p.User.Token.RevokeToken(t.Context(), &tokenv1.RevokeTokenRequest{
			Name: "e2e-proxy-revoked", EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: w.ID,
		}); err != nil {
			t.Fatal(err)
		}
		if code := queryLogsCode(t, revoked, w.ID); code != connect.CodeUnauthenticated {
			t.Errorf("QueryLogs with a revoked token: code %v", code)
		}
		scoped := w.MintToken(t, "e2e-proxy-scoped", tokenv1.Scope_SCOPE_READ)
		if code := queryLogsCode(t, scoped, otherWorkspace); code != connect.CodePermissionDenied {
			t.Errorf("QueryLogs for another workspace: code %v, want permission_denied", code)
		}
		if _, err := p.Clients(scoped).Observability.QueryLogs(t.Context(), queryLogsRequest(w.ID)); err != nil {
			t.Errorf("QueryLogs for the token's workspace: %v", err)
		}
	})

	t.Run("browser preflight and error responses", func(t *testing.T) {
		url := p.APIURL + "/loco.observability.v1.ObservabilityService/QueryLogs"
		preflight := map[string]string{originHeader: platform.DashboardOrigin, "Access-Control-Request-Method": "POST",
			"Access-Control-Request-Headers": "authorization,connect-protocol-version,connect-timeout-ms,content-type"}
		if origin := httpHeader(
			t,
			http.MethodOptions,
			url,
			preflight,
			nil,
			"Access-Control-Allow-Origin",
		); origin != platform.DashboardOrigin {
			t.Errorf("the preflight allows origin %q, want the dashboard", origin)
		}
		body := queryLogsJSON(t, w.ID)
		post := map[string]string{originHeader: platform.DashboardOrigin, contentType: "application/json"}
		if status := httpStatus(t, http.MethodPost, url, post, body); status != http.StatusUnauthorized {
			t.Errorf("an unauthenticated browser request: HTTP %d, want 401", status)
		}
		if origin := httpHeader(
			t,
			http.MethodPost,
			url,
			post,
			body,
			"Access-Control-Allow-Origin",
		); origin != platform.DashboardOrigin {
			t.Errorf("the browser cannot read the authentication error: Access-Control-Allow-Origin %q", origin)
		}
		other := map[string]string{originHeader: "https://other.e2e.test", "Access-Control-Request-Method": "POST"}
		if origin := httpHeader(t, http.MethodOptions, url, other, nil, "Access-Control-Allow-Origin"); origin != "" {
			t.Errorf("another origin is allowed: %q", origin)
		}
	})

	var token string
	t.Run("OTLP ingest requires the enrolled cluster's token", func(t *testing.T) {
		platform.Eventually(
			t,
			ingestTokenTimeout,
			"the agent to write the ingest token",
			func(ctx context.Context) error {
				var err error
				if token, err = p.IngestToken(ctx, p.CollectorNamespace()); err == nil && token == "" {
					err = errors.New("empty ingest token")
				}
				return err
			},
		)
		node, err := p.IngestToken(t.Context(), p.NodeCollectorNamespace())
		if err != nil {
			t.Fatal(err)
		}
		if node != token {
			t.Error("the node collector namespace holds another ingest token")
		}
		marker := "e2e-otlp-ingest-" + rand.Text()[:8]
		for name, header := range map[string]string{
			"no": "", "a forged": "Bearer " + forged(token), "a user session": "Bearer " + p.UserToken,
		} {
			if status, _ := postLogs(t, w.ID, marker, header); status != http.StatusUnauthorized {
				t.Errorf("OTLP with %s token: HTTP %d, want 401", name, status)
			}
		}
	})

	t.Run("OTLP ingest accepts placed workspaces only", func(t *testing.T) {
		dir := t.TempDir()
		const app = "e2e-otlp-ingest"
		platform.WriteLocoFile(
			t,
			dir,
			locofile.File{
				Partial:  app,
				Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage)},
			},
		)
		w.MustLoco(t, dir, "deploy", "--yes")
		marker := "e2e-otlp-ingest-" + rand.Text()[:8]
		status, rejected := postLogs(t, w.ID, marker, "Bearer "+token)
		if status != http.StatusOK || rejected != 0 {
			t.Errorf("logs of a placed workspace: HTTP %d with %d rejected, want 200 with none", status, rejected)
		}
		status, rejected = postLogs(t, otherWorkspace, "e2e-otlp-rejected-"+rand.Text()[:8], "Bearer "+token)
		if status != http.StatusOK || rejected != 1 {
			t.Errorf(
				"logs of an unplaced workspace: HTTP %d with %d rejected, want 200 with one rejected through partial success",
				status,
				rejected,
			)
		}
		auth := map[string]string{"Authorization": "Bearer " + token}
		protobuf := map[string]string{"Authorization": "Bearer " + token, contentType: "application/x-protobuf"}
		if status := httpStatus(
			t,
			http.MethodPost,
			p.OTLPURL+"/v1/logs",
			protobuf,
			[]byte{0xff, 0xff},
		); status != http.StatusBadRequest {
			t.Errorf("an undecodable OTLP request: HTTP %d, want 400", status)
		}
		auth[contentType] = "application/json"
		if status := httpStatus(
			t,
			http.MethodPost,
			p.OTLPURL+"/v1/logs",
			auth,
			[]byte("{}"),
		); status != http.StatusUnsupportedMediaType {
			t.Errorf("OTLP/JSON: HTTP %d, want 415", status)
		}
		query := p.Clients(w.MintToken(t, "e2e-proxy-ingest", tokenv1.Scope_SCOPE_READ))
		logs, err := query.Observability.QueryLogs(t.Context(), queryLogsRequest(w.ID))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range logs.GetEntries() {
			found = found || strings.Contains(entry.GetBody(), marker)
		}
		if !found {
			t.Errorf(
				"QueryLogs does not return the record written through OTLP ingest (%d entries)",
				len(logs.GetEntries()),
			)
		}
	})
}

func queryLogsRequest(workspaceID string) *observabilityv1.QueryLogsRequest {
	now := time.Now()
	return &observabilityv1.QueryLogsRequest{
		WorkspaceId: workspaceID,
		StartTime:   timestamppb.New(now.Add(-queryWindow)),
		EndTime:     timestamppb.New(now),
	}
}

func queryLogsJSON(t *testing.T, workspaceID string) []byte {
	t.Helper()
	body, err := protojson.Marshal(queryLogsRequest(workspaceID))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func queryLogsCode(t *testing.T, token, workspaceID string) connect.Code {
	t.Helper()
	_, err := p.Clients(token).Observability.QueryLogs(t.Context(), queryLogsRequest(workspaceID))
	return connect.CodeOf(err)
}

// forged keeps a token's header and claims and replaces its signature.
func forged(token string) string {
	signature := make([]byte, 64)
	if _, err := rand.Read(signature); err != nil {
		panic(err)
	}
	return token[:strings.LastIndex(token, ".")+1] + base64.RawURLEncoding.EncodeToString(signature)
}

// postLogs posts one OTLP/HTTP protobuf log record of workspaceID to the ingest proxy and returns
// the HTTP status and the records the proxy reports rejected.
func postLogs(t *testing.T, workspaceID, body, authorization string) (int, int64) {
	t.Helper()
	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr("service.name", "e2e-ingest")
	resourceLogs.Resource().Attributes().PutStr(workspaceAttribute, workspaceID)
	record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	// Whole seconds keep the record inside a query window that ends at the current second.
	record.SetTimestamp(pcommon.NewTimestampFromTime(time.Now().Truncate(time.Second)))
	record.SetSeverityText("INFO")
	record.Body().SetStr(body)
	payload, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{contentType: "application/x-protobuf"}
	if authorization != "" {
		headers["Authorization"] = authorization
	}
	response := do(t, http.MethodPost, p.OTLPURL+"/v1/logs", headers, payload)
	if response.status != http.StatusOK {
		return response.status, 0
	}
	decoded := plogotlp.NewExportResponse()
	if err := decoded.UnmarshalProto(response.body); err != nil {
		t.Fatalf("decode the export response: %v", err)
	}
	return response.status, decoded.PartialSuccess().RejectedLogRecords()
}

type httpResponse struct {
	status int
	header http.Header
	body   []byte
}

func do(t *testing.T, method, url string, headers map[string]string, body []byte) httpResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return httpResponse{status: response.StatusCode, header: response.Header, body: data}
}

func httpStatus(t *testing.T, method, url string, headers map[string]string, body []byte) int {
	t.Helper()
	return do(t, method, url, headers, body).status
}

func httpHeader(t *testing.T, method, url string, headers map[string]string, body []byte, name string) string {
	t.Helper()
	return do(t, method, url, headers, body).header.Get(name)
}
