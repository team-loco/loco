package resource

import (
	"net/http"
	"strings"
	"time"

	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/logstream"
)

const (
	buildPollInterval = time.Second
	buildLogDrain     = 3 * time.Second
)

type platformClients struct {
	Resources    func(host string) resourcev1connect.ResourceServiceClient
	Builds       func(host string) buildv1connect.BuildServiceClient
	Environments func(host string) environmentv1connect.EnvironmentServiceClient
	Access       func(host string) observabilityv1connect.ObservabilityAccessServiceClient
	Proxy        func(url string) observabilityv1connect.ObservabilityProxyServiceClient
	Upload       *http.Client
	PollInterval time.Duration
	LogDrain     time.Duration
}

func defaultPlatformClients() platformClients {
	upload := httputil.NewHTTPClient()
	return platformClients{
		Resources: func(host string) resourcev1connect.ResourceServiceClient {
			httpClient := httputil.NewHTTPClient()
			return resourcev1connect.NewResourceServiceClient(httpClient, host)
		},
		Builds: func(host string) buildv1connect.BuildServiceClient {
			httpClient := httputil.NewHTTPClient()
			return buildv1connect.NewBuildServiceClient(httpClient, host)
		},
		Environments: func(host string) environmentv1connect.EnvironmentServiceClient {
			httpClient := httputil.NewHTTPClient()
			return environmentv1connect.NewEnvironmentServiceClient(httpClient, host)
		},
		Access: func(host string) observabilityv1connect.ObservabilityAccessServiceClient {
			httpClient := httputil.NewHTTPClient()
			return observabilityv1connect.NewObservabilityAccessServiceClient(httpClient, host)
		},
		Proxy: func(url string) observabilityv1connect.ObservabilityProxyServiceClient {
			httpClient := httputil.NewHTTPClient()
			return observabilityv1connect.NewObservabilityProxyServiceClient(httpClient, url)
		},
		Upload:       upload,
		PollInterval: buildPollInterval,
		LogDrain:     buildLogDrain,
	}
}

func (c platformClients) logClient(token string) *logstream.Client {
	return &logstream.Client{Token: token, NewClient: c.Proxy}
}

func authHeaderFor(token string) string {
	return "Bearer " + token
}

func trimNewline(body string) string {
	return strings.TrimRight(body, "\r\n")
}
