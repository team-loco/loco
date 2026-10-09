package locofile

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tddExample = `version: 1
partial: web

services:
  web:
    dockerfile: web/Dockerfile
    context: .
    port: 3000
    health: { path: /healthz, interval: 30, timeout: 5, failThreshold: 3, startupGracePeriod: 15 }
    routing: { pathPrefix: /, idleTimeout: 60 }
    domains: [app.example.com]
    env: { LOG_LEVEL: info }
    secrets: [SESSION_KEY]
    regions:
      us-east-1: { cpu: 250m, memory: 512Mi, replicas: { min: 1, max: 3 } }
    environments:
      production:
        domains: [www.example.com]
        regions:
          us-east-1: { replicas: { min: 2, max: 6 }, autoscaling: { cpuTarget: 70 } }
      dev:
        enabled: false

  worker:
    image: ghcr.io/acme/worker:1.4.2
    regions:
      us-east-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
`

const minimalFile = `version: 1
partial: api
services:
  api:
    image: ghcr.io/acme/api:1
    regions:
      us-east-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
`

func TestParseTDDExample(t *testing.T) {
	file, err := Parse([]byte(tddExample))
	require.NoError(t, err)

	assert.Equal(t, 1, file.Version)
	assert.Equal(t, "web", file.Partial)
	require.Len(t, file.Services, 2)

	web := file.Services["web"]
	assert.Equal(t, "web/Dockerfile", web.Dockerfile)
	assert.Equal(t, ".", web.Context)
	assert.Equal(t, int32(3000), *web.Port)
	assert.Equal(t, "/healthz", web.Health.Path)
	assert.Equal(t, int32(15), *web.Health.StartupGracePeriod)
	assert.Equal(t, int32(60), *web.Routing.IdleTimeout)
	assert.Equal(t, []string{"app.example.com"}, web.Domains)
	assert.Equal(t, map[string]string{"LOG_LEVEL": "info"}, web.Env)
	assert.Equal(t, []string{"SESSION_KEY"}, web.Secrets)
	assert.Equal(t, "250m", web.Regions["us-east-1"].CPU)
	assert.Equal(t, int32(3), *web.Regions["us-east-1"].Replicas.Max)
	assert.Equal(t, []string{"www.example.com"}, web.Environments["production"].Domains)
	assert.Equal(t, int32(70), *web.Environments["production"].Regions["us-east-1"].Autoscaling.CPUTarget)
	assert.False(t, *web.Environments["dev"].Enabled)

	worker := file.Services["worker"]
	assert.Equal(t, "ghcr.io/acme/worker:1.4.2", worker.Image)
	assert.Nil(t, worker.Port)
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		err  error
		path string
	}{
		{
			name: "top-level databases",
			yaml: minimalFile + "databases:\n  main: { engine: postgres }\n",
			err:  ErrNotSupportedYet,
			path: "databases",
		},
		{
			name: "top-level caches",
			yaml: minimalFile + "caches: {}\n",
			err:  ErrNotSupportedYet,
			path: "caches",
		},
		{
			name: "top-level queues",
			yaml: minimalFile + "queues: {}\n",
			err:  ErrNotSupportedYet,
			path: "queues",
		},
		{
			name: "top-level buckets",
			yaml: minimalFile + "buckets: {}\n",
			err:  ErrNotSupportedYet,
			path: "buckets",
		},
		{
			name: "observability on a service",
			yaml: strings.Replace(minimalFile, "    image:", "    observability: { logging: true }\n    image:", 1),
			err:  ErrNotSupportedYet,
			path: "services.api.observability",
		},
		{
			name: "unknown key",
			yaml: minimalFile + "owner: me\n",
			err:  errUnknownKey,
		},
		{
			name: "wrong-case key",
			yaml: strings.Replace(minimalFile, "    image:", "    Port: 3000\n    image:", 1),
			err:  errUnknownKey,
		},
		{
			name: "unknown service key",
			yaml: strings.Replace(minimalFile, "    image:", "    replicas: 3\n    image:", 1),
			err:  errUnknownKey,
		},
		{
			name: "wrong version",
			yaml: strings.Replace(minimalFile, "version: 1", "version: 2", 1),
			err:  ErrUnsupportedVersion,
			path: "version",
		},
		{
			name: "missing partial",
			yaml: strings.Replace(minimalFile, "partial: api\n", "", 1),
			err:  ErrPartialRequired,
			path: keyPartial,
		},
		{
			name: "partial is not a DNS label",
			yaml: strings.Replace(minimalFile, "partial: api", "partial: My_API", 1),
			err:  ErrInvalidName,
			path: keyPartial,
		},
		{
			name: "partial too long",
			yaml: strings.Replace(minimalFile, "partial: api", "partial: "+strings.Repeat("a", 64), 1),
			err:  ErrInvalidName,
			path: keyPartial,
		},
		{
			name: "no services",
			yaml: "version: 1\npartial: api\nservices: {}\n",
			err:  ErrNoServices,
			path: "services",
		},
		{
			name: "service name is not a DNS label",
			yaml: strings.Replace(minimalFile, "  api:", "  Api:", 1),
			err:  ErrInvalidName,
			path: "services.Api",
		},
		{
			name: "dockerfile and image",
			yaml: strings.Replace(minimalFile, "    image:", "    dockerfile: Dockerfile\n    image:", 1),
			err:  ErrImageWithBuild,
			path: "services.api",
		},
		{
			name: "context with image",
			yaml: strings.Replace(minimalFile, "    image:", "    context: .\n    image:", 1),
			err:  ErrImageWithBuild,
			path: "services.api",
		},
		{
			name: "routing without port",
			yaml: strings.Replace(minimalFile, "    image:", "    routing: { pathPrefix: / }\n    image:", 1),
			err:  ErrPortRequired,
			path: "services.api.port",
		},
		{
			name: "no regions",
			yaml: "version: 1\npartial: api\nservices:\n  api:\n    image: ghcr.io/acme/api:1\n",
			err:  ErrNoRegions,
			path: "services.api.regions",
		},
		{
			name: "region without memory",
			yaml: strings.Replace(minimalFile, "memory: 256Mi, ", "", 1),
			err:  ErrRegionIncomplete,
			path: "services.api.regions.us-east-1",
		},
		{
			name: "region without replicas.max",
			yaml: strings.Replace(minimalFile, ", max: 1", "", 1),
			err:  ErrRegionIncomplete,
			path: "services.api.regions.us-east-1",
		},
		{
			name: "autoscaling with both targets",
			yaml: strings.Replace(minimalFile, "replicas: { min: 1, max: 1 }",
				"replicas: { min: 1, max: 1 }, autoscaling: { cpuTarget: 70, memoryTarget: 80 }", 1),
			err:  ErrAutoscalingTarget,
			path: "services.api.regions.us-east-1.autoscaling",
		},
		{
			name: "autoscaling without a target",
			yaml: strings.Replace(minimalFile, "replicas: { min: 1, max: 1 }",
				"replicas: { min: 1, max: 1 }, autoscaling: {}", 1),
			err:  ErrAutoscalingTarget,
			path: "services.api.regions.us-east-1.autoscaling",
		},
		{
			name: "autoscaling in an override with both targets",
			yaml: minimalFile + "    environments:\n      prod:\n        regions:\n" +
				"          us-east-1: { autoscaling: { cpuTarget: 70, memoryTarget: 80 } }\n",
			err:  ErrAutoscalingTarget,
			path: "services.api.environments.prod.regions.us-east-1.autoscaling",
		},
		{
			name: "environments inside an override",
			yaml: minimalFile + "    environments:\n      prod:\n        environments: {}\n",
			err:  ErrEnvironmentsInOverride,
			path: "services.api.environments.prod.environments",
		},
		{
			name: "duplicate key",
			yaml: minimalFile + "partial: again\n",
		},
		{
			name: "not a mapping",
			yaml: "- a\n- b\n",
			err:  ErrNotAnObject,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			require.Error(t, err)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			}
			if tt.path == "" {
				return
			}
			fieldErr, ok := errors.AsType[*FieldError](err)
			require.True(t, ok, "expected a FieldError, got %T: %v", err, err)
			assert.Equal(t, tt.path, fieldErr.Path)
		})
	}
}

func TestParseSourceBuildNeedsNoDockerfile(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "neither dockerfile nor image",
			yaml: strings.Replace(minimalFile, "    image: ghcr.io/acme/api:1\n", "", 1),
		},
		{
			name: "context only",
			yaml: strings.Replace(minimalFile, "    image: ghcr.io/acme/api:1\n", "    context: api\n", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := Parse([]byte(tt.yaml))
			require.NoError(t, err)
			assert.Empty(t, file.Services["api"].Image)
		})
	}
}

func TestParseReportsEveryStructuralError(t *testing.T) {
	_, err := Parse([]byte("version: 2\npartial: ''\nservices:\n  api:\n    image: x\n"))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnsupportedVersion)
	require.ErrorIs(t, err, ErrPartialRequired)
	assert.ErrorIs(t, err, ErrNoRegions)
}

func TestNotSupportedYetMessage(t *testing.T) {
	_, err := Parse([]byte(minimalFile + "buckets: {}\n"))
	require.EqualError(t, err, "buckets: not supported yet")
}

func TestParseSyntaxErrorCarriesLine(t *testing.T) {
	_, err := Parse([]byte("version: 1\npartial: api\nservices: [\n"))
	require.Error(t, err)
	parseErr, ok := errors.AsType[*ParseError](err)
	require.True(t, ok, "expected a ParseError, got %T: %v", err, err)
	assert.Equal(t, 3, parseErr.Line)
}

func TestParseUnknownKeyHasNoLine(t *testing.T) {
	_, err := Parse([]byte(minimalFile + "owner: me\n"))
	require.Error(t, err)
	parseErr, ok := errors.AsType[*ParseError](err)
	require.True(t, ok, "expected a ParseError, got %T: %v", err, err)
	assert.Equal(t, 0, parseErr.Line)
	assert.EqualError(t, err, "unknown key: owner")
}

func TestParseUnknownKeyNamesItsPath(t *testing.T) {
	_, err := Parse([]byte(strings.Replace(minimalFile, "    image:", "    Port: 3000\n    image:", 1)))
	require.EqualError(t, err, "unknown key: services.api.Port")
}
