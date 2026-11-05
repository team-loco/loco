package resource

import (
	"strings"
	"time"

	"github.com/team-loco/loco/internal/rpc"

	"github.com/team-loco/loco/cmd/internal/logstream"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
)

const (
	buildPollInterval = time.Second
	buildLogDrain     = 3 * time.Second
)

func logClient(host, token string) *logstream.Client {
	return &logstream.Client{URL: host, Token: token, NewClient: observabilityProxy}
}

func observabilityProxy(host, token string) observabilityv1connect.ObservabilityProxyServiceClient {
	return rpc.ForHost(host, token).ObservabilityProxy
}

func trimNewline(body string) string {
	return strings.TrimRight(body, "\r\n")
}
