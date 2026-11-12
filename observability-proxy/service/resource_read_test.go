package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
)

// resourceTestReader holds read on counterHTTPResource and nothing else.
const resourceTestReader = "resource-reader"

type proxyClient = observabilityv1connect.ObservabilityProxyServiceClient

type readCall func(context.Context, proxyClient, []string) error

func readCalls() map[string]readCall {
	return map[string]readCall{
		"QueryLogs": func(ctx context.Context, client proxyClient, ids []string) error {
			now := time.Now()
			_, err := client.QueryLogs(ctx, &observabilityv1.QueryLogsRequest{
				WorkspaceId: tailTestWorkspace, ResourceIds: ids,
				StartTime: timestamppb.New(now.Add(-time.Minute)), EndTime: timestamppb.New(now),
			})
			return err
		},
		"TailLogs": func(ctx context.Context, client proxyClient, ids []string) error {
			stream, err := client.TailLogs(ctx, &observabilityv1.TailLogsRequest{
				WorkspaceId: tailTestWorkspace, ResourceIds: ids,
			})
			if err != nil {
				return err
			}
			_, err = stream.Receive()
			return err
		},
		"QueryMetrics": func(ctx context.Context, client proxyClient, ids []string) error {
			req := counterRequest(tailTestWorkspace, time.Now())
			req.ResourceIds = ids
			_, err := client.QueryMetrics(ctx, req)
			return err
		},

		"GetTrace": func(ctx context.Context, client proxyClient, ids []string) error {
			_, err := client.GetTrace(
				ctx,
				&observabilityv1.GetTraceRequest{
					Scope:   &observabilityv1.TraceScope{WorkspaceId: tailTestWorkspace, ResourceIds: ids},
					TraceId: "0123456789abcdef0123456789abcdef",
				},
			)
			return err
		},
		"ListTraces": func(ctx context.Context, client proxyClient, ids []string) error {
			now := time.Now()
			_, err := client.ListTraces(ctx, &observabilityv1.ListTracesRequest{
				Scope:     &observabilityv1.TraceScope{WorkspaceId: tailTestWorkspace, ResourceIds: ids},
				StartTime: timestamppb.New(now.Add(-time.Minute)), EndTime: timestamppb.New(now),
			})
			return err
		},
	}
}

func TestResourceReadersNeedReadOnEveryNamedResource(t *testing.T) {
	// A request that passes authorization reaches the unavailable ClickHouse and fails as Internal.
	cases := []struct {
		name string
		ids  []string
		code connect.Code
	}{
		{"granted resource", []string{counterHTTPResource}, connect.CodeInternal},
		{"no resources named", nil, connect.CodePermissionDenied},
		{
			"granted and ungranted",
			[]string{counterHTTPResource, counterHTTPOtherResource},
			connect.CodePermissionDenied,
		},
		{"at the cap", slices.Repeat([]string{counterHTTPResource}, maxResourceReadChecks), connect.CodeInternal},
		{
			"over the cap",
			slices.Repeat([]string{counterHTTPResource}, maxResourceReadChecks+1),
			connect.CodePermissionDenied,
		},
	}
	client := traceTestClient(t, unavailableClickHouse(t))
	for rpc, call := range readCalls() {
		for _, tc := range cases {
			t.Run(rpc+"/"+tc.name, func(t *testing.T) {
				if err := call(resourceReaderContext(t), client, tc.ids); connect.CodeOf(err) != tc.code {
					t.Fatalf("error = %v, want %v", err, tc.code)
				}
			})
		}
	}
}

func TestResourceReadersSeeOnlyTheirResourceInTheNamedWorkspace(t *testing.T) {
	ch := newTestClickHouse(t)
	timestamp := time.Now().UTC().Add(-time.Minute)
	batch, err := ch.Conn().PrepareBatch(t.Context(), "INSERT INTO otel_logs (Timestamp, Body, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ resource, body string }{
		{counterHTTPResource, "granted"},
		{counterHTTPOtherResource, "ungranted"},
	} {
		attrs := map[string]string{
			counterHTTPWorkspaceAttribute: tailTestWorkspace,
			counterHTTPResourceAttribute:  row.resource,
		}
		if err := batch.Append(timestamp, row.body, attrs); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	client := traceTestClient(t, ch)

	for _, tc := range []struct {
		workspace string
		want      []string
	}{
		{tailTestWorkspace, []string{"granted"}},
		{traceTestOtherWorkspace, []string{}},
	} {
		t.Run(tc.workspace, func(t *testing.T) {
			response, err := client.QueryLogs(resourceReaderContext(t), &observabilityv1.QueryLogsRequest{
				WorkspaceId: tc.workspace, ResourceIds: []string{counterHTTPResource},
				StartTime: timestamppb.New(timestamp), EndTime: timestamppb.New(timestamp), Limit: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(response.GetEntries()))
			for _, entry := range response.GetEntries() {
				got = append(got, entry.GetBody())
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("bodies = %v, want %v", got, tc.want)
			}
		})
	}
}

func resourceReaderContext(t *testing.T) context.Context {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+resourceTestReader)
	return ctx
}
