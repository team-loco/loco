package resource

import (
	"net/http"
	"strings"
	"time"

	"github.com/team-loco/loco/internal/rpc"

	"github.com/team-loco/loco/cmd/internal/logstream"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/httputil"
)

const (
	buildPollInterval = time.Second
	buildLogDrain     = 3 * time.Second
)

type platformClients struct {
	Builds       func(host, token string) buildv1connect.BuildServiceClient
	Proxy        func(url, token string) observabilityv1connect.ObservabilityProxyServiceClient
	Upload       *http.Client
	PollInterval time.Duration
	LogDrain     time.Duration
}

func defaultPlatformClients() platformClients {
	upload := httputil.NewHTTPClient()
	return platformClients{
		Builds: func(host, token string) buildv1connect.BuildServiceClient {
			buildClients := rpc.ForHost(host, token)
			return buildClients.Build
		},
		Proxy: func(url, token string) observabilityv1connect.ObservabilityProxyServiceClient {
			observabilityProxyClients := rpc.ForHost(url, token)
			return observabilityProxyClients.ObservabilityProxy
		},
		Upload:       upload,
		PollInterval: buildPollInterval,
		LogDrain:     buildLogDrain,
	}
}

func (c platformClients) logClient(host, token string) *logstream.Client {
	return &logstream.Client{URL: host, Token: token, NewClient: c.Proxy}
}

func trimNewline(body string) string {
	return strings.TrimRight(body, "\r\n")
}
