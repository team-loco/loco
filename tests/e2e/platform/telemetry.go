package platform

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
)

// workspaceAttribute matches the one observability-proxy/pkg/ingest/tenancy.go filters on.
const workspaceAttribute = "loco.workspace.id"

// OTLPLogs is an OTLP/HTTP protobuf export of one INFO log record of workspaceID.
func OTLPLogs(tb testing.TB, workspaceID, body string) []byte {
	tb.Helper()
	logs := plog.NewLogs()
	resource := logs.ResourceLogs().AppendEmpty()
	resource.Resource().Attributes().PutStr("service.name", "e2e-ingest")
	resource.Resource().Attributes().PutStr(workspaceAttribute, workspaceID)
	record := resource.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	// Whole seconds keep the record inside a query window that ends at the current second.
	record.SetTimestamp(pcommon.NewTimestampFromTime(time.Now().Truncate(time.Second)))
	record.SetSeverityText("INFO")
	record.Body().SetStr(body)
	payload, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	if err != nil {
		tb.Fatal(err)
	}
	return payload
}

// CollectorNamespace is the namespace of the OTLP gateway and cluster collectors.
func (p *Platform) CollectorNamespace() string { return p.values.collectorNS }

// NodeCollectorNamespace is the namespace of the node collectors.
func (p *Platform) NodeCollectorNamespace() string { return p.values.nodeCollectorNS }

// CollectorName is the StatefulSet and ConfigMap name of the OTLP gateway collector.
func (p *Platform) CollectorName() string { return p.values.collectorName }

// CollectorEndpoint is the OTLP/HTTP endpoint applications in the cluster export to.
func (p *Platform) CollectorEndpoint() string {
	return fmt.Sprintf(
		"http://%s.%s.svc.cluster.local:%d",
		p.values.collectorName,
		p.values.collectorNS,
		p.values.collectorHTTPPort,
	)
}

// IngestToken reads the ingest token the agent wrote into a collector namespace.
func (p *Platform) IngestToken(ctx context.Context, namespace string) (string, error) {
	out, err := p.Kubectl(
		ctx,
		"-n",
		namespace,
		"get",
		"secret",
		p.values.ingestTokenSecret,
		"-o",
		"jsonpath={.data."+p.values.ingestTokenKey+"}",
	)
	if err != nil {
		return "", err
	}
	token, err := base64.StdEncoding.DecodeString(out)
	return string(token), err
}

// ClickHouse runs a query against the observability database and returns its tab-separated rows.
func (p *Platform) ClickHouse(ctx context.Context, query string) ([][]string, error) {
	out, err := p.ClickHouseClient(ctx, query, "--database", p.values.clickhouseDB, "--format", "TabSeparated")
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for line := range strings.Lines(out) {
		if line = strings.TrimRight(line, "\n"); line != "" {
			rows = append(rows, strings.Split(line, "\t"))
		}
	}
	return rows, nil
}

// ClickHouseCount runs a query whose one row is a count.
func (p *Platform) ClickHouseCount(ctx context.Context, query string) (int, error) {
	rows, err := p.ClickHouse(ctx, query)
	if err != nil {
		return 0, err
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return 0, fmt.Errorf("%q returned %v, want one count", query, rows)
	}
	return strconv.Atoi(rows[0][0])
}
