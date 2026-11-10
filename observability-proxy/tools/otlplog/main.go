// Command otlplog writes the OTLP/HTTP protobuf log export the e2e suites post to the ingest proxy,
// and with -rejected reads the rejected record count from the proxy's response.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
)

const (
	serviceName = "e2e-ingest"
	severity    = "INFO"
	// workspaceAttribute matches the one observability-proxy/pkg/ingest/tenancy.go filters on.
	workspaceAttribute = "loco.workspace.id"
)

var errMissingFlags = errors.New("-workspace and -body are required")

func exportRequest(workspace, body string, now time.Time) ([]byte, error) {
	if workspace == "" || body == "" {
		return nil, errMissingFlags
	}
	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr("service.name", serviceName)
	resourceLogs.Resource().Attributes().PutStr(workspaceAttribute, workspace)
	record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	// Whole seconds keep the record inside a query window that ends at the current second.
	record.SetTimestamp(pcommon.NewTimestampFromTime(now.Truncate(time.Second)))
	record.SetSeverityText(severity)
	record.Body().SetStr(body)
	return plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
}

func rejectedRecords(response []byte) (int64, error) {
	decoded := plogotlp.NewExportResponse()
	if err := decoded.UnmarshalProto(response); err != nil {
		return 0, fmt.Errorf("decode export response: %w", err)
	}
	return decoded.PartialSuccess().RejectedLogRecords(), nil
}

func main() {
	workspace := flag.String("workspace", "", "loco.workspace.id resource attribute of the log record")
	body := flag.String("body", "", "body of the log record")
	rejected := flag.Bool("rejected", false, "read an export response on stdin and print its rejected log record count")
	flag.Parse()
	if *rejected {
		response, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		count, err := rejectedRecords(response)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(count)
		return
	}
	request, err := exportRequest(*workspace, *body, time.Now())
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stdout.Write(request); err != nil {
		log.Fatal(err)
	}
}
