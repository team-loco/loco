package logstream

import (
	"strings"

	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

func ForHost(host, token string) *Client {
	return &Client{URL: host, Token: token, NewClient: observabilityProxy}
}

func observabilityProxy(host, token string) observabilityv1connect.ObservabilityProxyServiceClient {
	return rpc.ForHost(host, token).ObservabilityProxy
}

func TrimNewline(body string) string {
	return strings.TrimRight(body, "\r\n")
}
