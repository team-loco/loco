package ingest

import (
	"context"
	"crypto/rand"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const (
	testRemovedWorkspaceID = "33333333-4444-5555-6666-777777777777"
	testNeverPlacedID      = "44444444-5555-6666-7777-888888888888"
	testForeignAudience    = "loco-api"
	testForgedClusterID    = "forged-cluster"
)

func signedWith(t *testing.T, key ingesttoken.SigningKey, audience string, issuedAt time.Time) string {
	t.Helper()
	signingKey := jose.SigningKey{Algorithm: jose.EdDSA, Key: jose.JSONWebKey{Key: key.Key, KeyID: key.ID}}
	signer, err := jose.NewSigner(signingKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.Claims{
		Subject:  testClusterID,
		Audience: jwt.Audience{audience},
		IssuedAt: jwt.NewNumericDate(issuedAt),
		Expiry:   jwt.NewNumericDate(issuedAt.Add(testTokenLifetime)),
		ID:       rand.Text(),
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestHTTPAuthStatusMapping(t *testing.T) {
	expired := time.Now().Add(-2 * testTokenLifetime)
	for _, test := range []struct {
		name          string
		authorization func(*controlPlane) string
		setup         func(*ingesttest.State)
		wantHTTP      int
		wantCode      codes.Code
	}{
		{
			name:          "missing token",
			authorization: func(*controlPlane) string { return "" },
			wantHTTP:      http.StatusUnauthorized, wantCode: codes.Unauthenticated,
		},
		{
			name:          "not a bearer token",
			authorization: func(cp *controlPlane) string { return "Basic " + cp.token(time.Now()) },
			wantHTTP:      http.StatusUnauthorized, wantCode: codes.Unauthenticated,
		},
		{
			name: "forged signature",
			authorization: func(cp *controlPlane) string {
				forger := newSigningKey(cp.t, testCurrentKeyID)
				return bearerPrefix + signedWith(cp.t, forger, ingesttoken.Audience, time.Now())
			},
			wantHTTP: http.StatusUnauthorized, wantCode: codes.Unauthenticated,
		},
		{
			name: "wrong audience",
			authorization: func(cp *controlPlane) string {
				return bearerPrefix + signedWith(cp.t, cp.SigningKey(), testForeignAudience, time.Now())
			},
			wantHTTP: http.StatusUnauthorized, wantCode: codes.Unauthenticated,
		},
		{
			name: "expired with a wrong audience",
			authorization: func(cp *controlPlane) string {
				return bearerPrefix + signedWith(cp.t, cp.SigningKey(), testForeignAudience, expired)
			},
			wantHTTP: http.StatusUnauthorized, wantCode: codes.Unauthenticated,
		},
		{
			name: "expired",
			authorization: func(cp *controlPlane) string {
				return bearerPrefix + cp.token(expired)
			},
			wantHTTP: http.StatusServiceUnavailable, wantCode: codes.Unavailable,
		},
		{
			name:          "revoked cluster",
			authorization: func(cp *controlPlane) string { return cp.bearer() },
			setup:         func(s *ingesttest.State) { s.Revoked = true },
			wantHTTP:      http.StatusForbidden, wantCode: codes.PermissionDenied,
		},
		{
			name:          "control plane unavailable",
			authorization: func(cp *controlPlane) string { return cp.bearer() },
			setup:         func(s *ingesttest.State) { s.Outage = true },
			wantHTTP:      http.StatusServiceUnavailable, wantCode: codes.Unavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, recorder, cp := newTestService(t, nil)
			if test.setup != nil {
				cp.Update(test.setup)
			}
			response := post(t, service, test.authorization(cp), protobufContentType, "", protoBody(t))
			if response.Code != test.wantHTTP {
				t.Fatalf("status = %d, want %d (%s)", response.Code, test.wantHTTP, response.Body.String())
			}
			message := decodeStatus(t, response)
			if codes.Code(message.GetCode()) != test.wantCode {
				t.Fatalf("status code = %s, want %s", codes.Code(message.GetCode()), test.wantCode)
			}
			retryAfter := response.Header().Get(retryAfterHeader)
			wantRetryAfter := ""
			if test.wantHTTP == http.StatusServiceUnavailable {
				wantRetryAfter = strconv.Itoa(int(testRetryAfter.Seconds()))
			}
			if retryAfter != wantRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", retryAfter, wantRetryAfter)
			}
			if len(recorder.received) != 0 {
				t.Fatalf("consumer received %d requests, want none", len(recorder.received))
			}
		})
	}
}

func TestGRPCAuthStatusMapping(t *testing.T) {
	for _, test := range []struct {
		name          string
		authorization func(*controlPlane) string
		setup         func(*ingesttest.State)
		want          codes.Code
	}{
		{
			name:          "missing token",
			authorization: func(*controlPlane) string { return "" },
			want:          codes.Unauthenticated,
		},
		{
			name: "forged signature",
			authorization: func(cp *controlPlane) string {
				forger := newSigningKey(cp.t, testCurrentKeyID)
				return bearerPrefix + signedWith(cp.t, forger, ingesttoken.Audience, time.Now())
			},
			want: codes.Unauthenticated,
		},
		{
			name: "expired",
			authorization: func(cp *controlPlane) string {
				return bearerPrefix + cp.token(time.Now().Add(-2*testTokenLifetime))
			},
			want: codes.Unavailable,
		},
		{
			name:          "revoked cluster",
			authorization: func(cp *controlPlane) string { return cp.bearer() },
			setup:         func(s *ingesttest.State) { s.Revoked = true },
			want:          codes.PermissionDenied,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, recorder, cp := newTestService(t, nil)
			if test.setup != nil {
				cp.Update(test.setup)
			}
			ctx := t.Context()
			if authorization := test.authorization(cp); authorization != "" {
				ctx = metadata.AppendToOutgoingContext(ctx, authorizationMetadata, authorization)
			}
			_, err := grpcClient(t, service).Export(ctx, testLogsRequest())
			if got := status.Code(err); got != test.want {
				t.Fatalf("code = %s, want %s (%v)", got, test.want, err)
			}
			if test.want == codes.Unavailable && !hasRetryInfo(err) {
				t.Fatalf("retryable error %v carries no RetryInfo", err)
			}
			if len(recorder.received) != 0 {
				t.Fatalf("consumer received %d requests, want none", len(recorder.received))
			}
		})
	}
}

func hasRetryInfo(err error) bool {
	for _, detail := range status.Convert(err).Details() {
		if info, ok := detail.(*errdetails.RetryInfo); ok && info.GetRetryDelay().AsDuration() == testRetryAfter {
			return true
		}
	}
	return false
}

func TestJWKSRefreshesOnAnUnknownKeyAtMostOncePerInterval(t *testing.T) {
	cp := newPlacedControlPlane(t)
	gate := cp.gate()
	gate.cfg.JWKSRefreshInterval = time.Hour
	ctx := t.Context()
	if _, err := gate.Admit(ctx, cp.bearer()); err != nil {
		t.Fatal(err)
	}
	rotated := newSigningKey(t, testRotatedKeyID)
	cp.Update(func(s *ingesttest.State) { s.Keys = append([]ingesttoken.SigningKey{rotated}, s.Keys...) })
	gate.keysFetched = time.Now().Add(-2 * time.Hour)
	if _, err := gate.Admit(ctx, cp.bearer()); err != nil {
		t.Fatalf("token from the rotated key: %v", err)
	}
	unpublished := newSigningKey(t, "unpublished")
	_, err := gate.Admit(ctx, bearerPrefix+signedWith(t, unpublished, ingesttoken.Audience, time.Now()))
	if !strings.Contains(err.Error(), ErrAuthUnavailable.Error()) {
		t.Fatalf("unknown key within the refresh interval = %v, want a retryable failure", err)
	}
	if _, jwks := cp.Calls(); jwks != 2 {
		t.Fatalf("JWKS fetched %d times, want 2", jwks)
	}
	gate.keysFetched = time.Now().Add(-2 * time.Hour)
	_, err = gate.Admit(ctx, bearerPrefix+signedWith(t, unpublished, ingesttoken.Audience, time.Now()))
	if !strings.Contains(err.Error(), ErrUnauthenticated.Error()) {
		t.Fatalf("unknown key after a refresh = %v, want unauthenticated", err)
	}
}

type tenancyCase struct {
	workspaceID string
	at          time.Time
	accepted    bool
}

func tenancyCases(now time.Time) []tenancyCase {
	return []tenancyCase{
		{workspaceID: testWorkspaceID, at: now, accepted: true},
		{workspaceID: testWorkspaceID, at: now.Add(-testPlacedFor - testClockSkew/2), accepted: true},
		{workspaceID: testWorkspaceID, at: now.Add(-testPlacedFor - 2*testClockSkew)},
		{workspaceID: testRemovedWorkspaceID, at: now.Add(-testPlacedFor / 2), accepted: true},
		{workspaceID: testRemovedWorkspaceID, at: now},
		{workspaceID: testOtherWorkspaceID, at: now},
		{at: now, accepted: true},
	}
}

const (
	wantRejected  = 3
	wantAccepted  = 4
	wantNotPlaced = 1
	wantOutside   = 2
)

func tenancyControlPlane(t *testing.T, now time.Time) *controlPlane {
	t.Helper()
	cp := newControlPlane(t)
	cp.Place(testWorkspaceID, now.Add(-testPlacedFor), time.Time{})
	cp.Place(testRemovedWorkspaceID, now.Add(-testPlacedFor), now.Add(-testPlacedFor/4))
	return cp
}

func tagWorkspace(resource pcommon.Resource, workspaceID string) {
	resource.Attributes().PutStr(clusterAttribute, testForgedClusterID)
	if workspaceID != "" {
		resource.Attributes().PutStr(workspaceAttribute, workspaceID)
	}
}

func tenancyLogs(now time.Time) plog.Logs {
	logs := plog.NewLogs()
	for _, c := range tenancyCases(now) {
		resourceLogs := logs.ResourceLogs().AppendEmpty()
		tagWorkspace(resourceLogs.Resource(), c.workspaceID)
		record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		record.SetObservedTimestamp(pcommon.NewTimestampFromTime(c.at))
		record.Body().SetStr(strconv.FormatBool(c.accepted))
	}
	return logs
}

func tenancyTraces(now time.Time) ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans()
	for _, c := range tenancyCases(now) {
		spans := resourceSpans.AppendEmpty()
		tagWorkspace(spans.Resource(), c.workspaceID)
		span := spans.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		span.SetStartTimestamp(pcommon.NewTimestampFromTime(c.at))
		span.SetName(strconv.FormatBool(c.accepted))
	}
	return traces
}

func tenancyMetrics(now time.Time) pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	byWorkspace := map[string]pmetric.MetricSlice{}
	for _, c := range tenancyCases(now) {
		slice, ok := byWorkspace[c.workspaceID]
		if !ok {
			resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
			tagWorkspace(resourceMetrics.Resource(), c.workspaceID)
			slice = resourceMetrics.ScopeMetrics().AppendEmpty().Metrics()
			byWorkspace[c.workspaceID] = slice
		}
		metric := slice.AppendEmpty()
		metric.SetName(strconv.FormatBool(c.accepted))
		point := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
		point.SetTimestamp(pcommon.NewTimestampFromTime(c.at))
		gauge := slice.AppendEmpty()
		gauge.SetName(strconv.FormatBool(c.accepted))
		gaugePoint := gauge.SetEmptyGauge().DataPoints().AppendEmpty()
		gaugePoint.SetTimestamp(pcommon.NewTimestampFromTime(c.at))
	}
	return metrics
}

type capturingConsumers struct {
	logs    []plog.Logs
	traces  []ptrace.Traces
	metrics []pmetric.Metrics
}

func (c *capturingConsumers) consumers(t *testing.T) Consumers {
	t.Helper()
	logs, err := consumer.NewLogs(func(_ context.Context, l plog.Logs) error {
		c.logs = append(c.logs, l)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	traces, err := consumer.NewTraces(func(_ context.Context, tr ptrace.Traces) error {
		c.traces = append(c.traces, tr)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := consumer.NewMetrics(func(_ context.Context, m pmetric.Metrics) error {
		c.metrics = append(c.metrics, m)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return Consumers{Logs: logs, Traces: traces, Metrics: metrics}
}

func requireClusterStamp(t *testing.T, resource pcommon.Resource) {
	t.Helper()
	value, ok := resource.Attributes().Get(clusterAttribute)
	if !ok || value.Str() != testClusterID {
		t.Fatalf(
			"%s = %v, want the token's cluster %s over the client's value",
			clusterAttribute,
			value.AsRaw(),
			testClusterID,
		)
	}
}

func requireRejectionMetrics(t *testing.T, service *Service, factor int) {
	t.Helper()
	for reason, want := range map[verdict]int{
		rejectNotPlaced:       wantNotPlaced,
		rejectOutsideOfWindow: wantOutside,
	} {
		counter := service.rejected.WithLabelValues(testClusterID, string(reason))
		if got := testutil.ToFloat64(counter); got != float64(want*factor) {
			t.Errorf("%s{%s} = %v, want %d", rejectedRecordsMetric, reason, got, want*factor)
		}
	}
}

func requireRejectionMessage(t *testing.T, message string) {
	t.Helper()
	for _, part := range []string{
		string(rejectNotPlaced) + "=1",
		string(rejectOutsideOfWindow) + "=2",
	} {
		if !strings.Contains(message, part) {
			t.Fatalf("partial success message %q lacks %q", message, part)
		}
	}
}

func TestTenancyFilterDropsRecordsOutsideThePlacementWindows(t *testing.T) {
	now := time.Now()
	cp := tenancyControlPlane(t, now)
	captured := &capturingConsumers{}
	service := newGatedService(captured.consumers(t), cp)
	admission, err := service.gate.Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	logsResponse, err := service.exportLogs(ctx, admission, plogotlp.NewExportRequestFromLogs(tenancyLogs(now)))
	if err != nil {
		t.Fatal(err)
	}
	if got := logsResponse.PartialSuccess().RejectedLogRecords(); got != wantRejected {
		t.Fatalf("rejected log records = %d, want %d", got, wantRejected)
	}
	requireRejectionMessage(t, logsResponse.PartialSuccess().ErrorMessage())
	if len(captured.logs) != 1 || captured.logs[0].LogRecordCount() != wantAccepted {
		t.Fatalf("stored %v, want %d log records", captured.logs, wantAccepted)
	}
	for i := range captured.logs[0].ResourceLogs().Len() {
		resourceLogs := captured.logs[0].ResourceLogs().At(i)
		record := resourceLogs.ScopeLogs().At(0).LogRecords().At(0)
		if record.Body().Str() != strconv.FormatBool(true) {
			t.Fatalf("stored a record that should be rejected: %v", record.Body().Str())
		}
		requireClusterStamp(t, resourceLogs.Resource())
	}

	tracesRequest := ptraceotlp.NewExportRequestFromTraces(tenancyTraces(now))
	tracesResponse, err := service.exportTraces(ctx, admission, tracesRequest)
	if err != nil {
		t.Fatal(err)
	}
	if got := tracesResponse.PartialSuccess().RejectedSpans(); got != wantRejected {
		t.Fatalf("rejected spans = %d, want %d", got, wantRejected)
	}
	if len(captured.traces) != 1 || captured.traces[0].SpanCount() != wantAccepted {
		t.Fatalf("stored %d spans, want %d", captured.traces[0].SpanCount(), wantAccepted)
	}
	for i := range captured.traces[0].ResourceSpans().Len() {
		requireClusterStamp(t, captured.traces[0].ResourceSpans().At(i).Resource())
	}

	metricsRequest := pmetricotlp.NewExportRequestFromMetrics(tenancyMetrics(now))
	metricsResponse, err := service.exportMetrics(ctx, admission, metricsRequest)
	if err != nil {
		t.Fatal(err)
	}
	if got := metricsResponse.PartialSuccess().RejectedDataPoints(); got != 2*wantRejected {
		t.Fatalf("rejected data points = %d, want %d", got, 2*wantRejected)
	}
	if len(captured.metrics) != 1 || captured.metrics[0].DataPointCount() != 2*wantAccepted {
		t.Fatalf("stored %d data points, want %d", captured.metrics[0].DataPointCount(), 2*wantAccepted)
	}
	for i := range captured.metrics[0].ResourceMetrics().Len() {
		requireClusterStamp(t, captured.metrics[0].ResourceMetrics().At(i).Resource())
	}
	requireRejectionMetrics(t, service, 1+1+2)
}

func TestFullyAcceptedRequestsReportNoPartialSuccess(t *testing.T) {
	service, recorder, cp := newTestService(t, nil)
	response := post(t, service, cp.bearer(), protobufContentType, "", protoBody(t))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	decoded := plogotlp.NewExportResponse()
	if err := decoded.UnmarshalProto(response.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if decoded.PartialSuccess().RejectedLogRecords() != 0 || decoded.PartialSuccess().ErrorMessage() != "" {
		t.Fatalf("partial success = %v", decoded.PartialSuccess())
	}
	if len(recorder.received) != 1 {
		t.Fatalf("consumer received %d requests", len(recorder.received))
	}
}

func TestHTTPRejectionsArriveAsPartialSuccess(t *testing.T) {
	now := time.Now()
	cp := tenancyControlPlane(t, now)
	captured := &capturingConsumers{}
	service := newGatedService(captured.consumers(t), cp)
	body, err := plogotlp.NewExportRequestFromLogs(tenancyLogs(now)).MarshalProto()
	if err != nil {
		t.Fatal(err)
	}
	response := post(t, service, cp.bearer(), protobufContentType, "", body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with partial success", response.Code)
	}
	decoded := plogotlp.NewExportResponse()
	if err := decoded.UnmarshalProto(response.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if decoded.PartialSuccess().RejectedLogRecords() != wantRejected {
		t.Fatalf("partial success = %v", decoded.PartialSuccess())
	}
	requireRejectionMessage(t, decoded.PartialSuccess().ErrorMessage())
}

func logsFor(workspaceIDs ...string) plogotlp.ExportRequest {
	logs := plog.NewLogs()
	now := pcommon.NewTimestampFromTime(time.Now())
	for _, workspaceID := range workspaceIDs {
		resourceLogs := logs.ResourceLogs().AppendEmpty()
		tagWorkspace(resourceLogs.Resource(), workspaceID)
		resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().SetTimestamp(now)
	}
	return plogotlp.NewExportRequestFromLogs(logs)
}

func exportFor(t *testing.T, service *Service, cp *controlPlane, workspaceIDs ...string) plogotlp.ExportResponse {
	t.Helper()
	admission, err := service.gate.Admit(t.Context(), cp.bearer())
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.exportLogs(t.Context(), admission, logsFor(workspaceIDs...))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestPlacementCacheRefetchesOnceOnAMiss(t *testing.T) {
	cp := newPlacedControlPlane(t)
	captured := &capturingConsumers{}
	service := newGatedService(captured.consumers(t), cp)

	exportFor(t, service, cp, testWorkspaceID)
	exportFor(t, service, cp, testWorkspaceID)
	if list, _ := cp.Calls(); list != 1 {
		t.Fatalf("placements fetched %d times for cached hits, want 1", list)
	}

	cp.Place(testOtherWorkspaceID, time.Now().Add(-time.Minute), time.Time{})
	response := exportFor(t, service, cp, testOtherWorkspaceID)
	if response.PartialSuccess().RejectedLogRecords() != 0 {
		t.Fatalf("newly placed workspace rejected: %v", response.PartialSuccess())
	}
	if list, _ := cp.Calls(); list != 2 {
		t.Fatalf("placements fetched %d times, want a refetch on the miss", list)
	}

	response = exportFor(t, service, cp, testNeverPlacedID, testNeverPlacedID, testNeverPlacedID)
	if response.PartialSuccess().RejectedLogRecords() != 3 {
		t.Fatalf("never placed workspace: %v", response.PartialSuccess())
	}
	if list, _ := cp.Calls(); list != 3 {
		t.Fatalf("placements fetched %d times, want one refetch per request", list)
	}

	service.gate.cfg.RefetchInterval = time.Hour
	exportFor(t, service, cp, testNeverPlacedID)
	if list, _ := cp.Calls(); list != 3 {
		t.Fatalf("placements fetched %d times, want no refetch within the refetch interval", list)
	}
}

func TestRevocationSeenOnRefetchFailsTheRequest(t *testing.T) {
	service, recorder, cp := newTestService(t, nil)
	if response := post(
		t,
		service,
		cp.bearer(),
		protobufContentType,
		"",
		protoBody(t),
	); response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	cp.Update(func(s *ingesttest.State) { s.Revoked = true })
	body, err := logsFor(testOtherWorkspaceID).MarshalProto()
	if err != nil {
		t.Fatal(err)
	}
	response := post(t, service, cp.bearer(), protobufContentType, "", body)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 once the refetch sees the revocation", response.Code)
	}
	if len(recorder.received) != 1 {
		t.Fatalf("consumer received %d requests, want 1", len(recorder.received))
	}
}
