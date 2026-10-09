package locofile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	keyPort     = "port"
	keyImage    = "image"
	keyDomains  = "domains"
	keyEnv      = "env"
	keyRegions  = "regions"
	keyHealth   = "health"
	keyMax      = "max"
	keyPath     = "path"
	keyReplicas = "replicas"
	healthPath  = "/healthz"
	healthNone  = "none"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name     string
		service  map[string]any
		override map[string]any
		want     map[string]any
	}{
		{
			name:     "empty override keeps the service",
			service:  map[string]any{keyPort: 3000},
			override: map[string]any{},
			want:     map[string]any{keyPort: 3000},
		},
		{
			name:     "scalar replaces",
			service:  map[string]any{keyPort: 3000},
			override: map[string]any{keyPort: 8080},
			want:     map[string]any{keyPort: 8080},
		},
		{
			name:     "new key is added",
			service:  map[string]any{keyPort: 3000},
			override: map[string]any{keyImage: "x"},
			want:     map[string]any{keyPort: 3000, keyImage: "x"},
		},
		{
			name:     "null removes a key",
			service:  map[string]any{keyPort: 3000, keyImage: "x"},
			override: map[string]any{keyImage: nil},
			want:     map[string]any{keyPort: 3000},
		},
		{
			name:     "null on a missing key is a no-op",
			service:  map[string]any{keyPort: 3000},
			override: map[string]any{keyImage: nil},
			want:     map[string]any{keyPort: 3000},
		},
		{
			name:     "lists replace whole",
			service:  map[string]any{keyDomains: []any{"a.example.com", "b.example.com"}},
			override: map[string]any{keyDomains: []any{"c.example.com"}},
			want:     map[string]any{keyDomains: []any{"c.example.com"}},
		},
		{
			name:     "objects merge recursively",
			service:  map[string]any{keyEnv: map[string]any{"A": "1", "B": "2"}},
			override: map[string]any{keyEnv: map[string]any{"B": "3", "C": "4"}},
			want:     map[string]any{keyEnv: map[string]any{"A": "1", "B": "3", "C": "4"}},
		},
		{
			name:     "null removes a nested key",
			service:  map[string]any{keyEnv: map[string]any{"A": "1", "B": "2"}},
			override: map[string]any{keyEnv: map[string]any{"B": nil}},
			want:     map[string]any{keyEnv: map[string]any{"A": "1"}},
		},
		{
			name: "regions merge two levels deep",
			service: map[string]any{keyRegions: map[string]any{
				testRegion: map[string]any{"cpu": "250m", keyReplicas: map[string]any{"min": 1, keyMax: 3}},
			}},
			override: map[string]any{keyRegions: map[string]any{
				testRegion: map[string]any{
					keyReplicas:   map[string]any{keyMax: 6},
					"autoscaling": map[string]any{"cpuTarget": 70},
				},
			}},
			want: map[string]any{keyRegions: map[string]any{
				testRegion: map[string]any{
					"cpu":         "250m",
					keyReplicas:   map[string]any{"min": 1, keyMax: 6},
					"autoscaling": map[string]any{"cpuTarget": 70},
				},
			}},
		},
		{
			name:     "object replaces a scalar",
			service:  map[string]any{keyHealth: healthNone},
			override: map[string]any{keyHealth: map[string]any{keyPath: healthPath}},
			want:     map[string]any{keyHealth: map[string]any{keyPath: healthPath}},
		},
		{
			name:     "scalar replaces an object",
			service:  map[string]any{keyHealth: map[string]any{keyPath: healthPath}},
			override: map[string]any{keyHealth: healthNone},
			want:     map[string]any{keyHealth: healthNone},
		},
		{
			name:     "nil service",
			service:  nil,
			override: map[string]any{keyPort: 1},
			want:     map[string]any{keyPort: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge(tt.service, tt.override)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	service := map[string]any{keyEnv: map[string]any{"A": "1"}, keyImage: "x"}
	override := map[string]any{keyEnv: map[string]any{"B": "2"}, keyImage: nil}
	Merge(service, override)
	assert.Equal(t, map[string]any{keyEnv: map[string]any{"A": "1"}, keyImage: "x"}, service)
	assert.Equal(t, map[string]any{keyEnv: map[string]any{"B": "2"}, keyImage: nil}, override)
}

func TestResolveTDDExample(t *testing.T) {
	file, err := Parse([]byte(tddExample))
	require.NoError(t, err)

	production, err := Resolve(file, "production")
	require.NoError(t, err)
	require.Len(t, production, 2)
	web := production["web"]
	assert.Equal(t, []string{"www.example.com"}, web.Domains)
	assert.Equal(t, "250m", web.Regions[testRegion].CPU)
	assert.Equal(t, int32(2), *web.Regions[testRegion].Replicas.Min)
	assert.Equal(t, int32(6), *web.Regions[testRegion].Replicas.Max)
	assert.Equal(t, int32(70), *web.Regions[testRegion].Autoscaling.CPUTarget)
	assert.Equal(t, int32(3000), *web.Port)
	assert.Nil(t, web.Environments)
	assert.Equal(t, file.Services["worker"], production["worker"])

	dev, err := Resolve(file, "dev")
	require.NoError(t, err)
	require.Len(t, dev, 1)
	assert.Contains(t, dev, "worker")

	staging, err := Resolve(file, "staging")
	require.NoError(t, err)
	require.Len(t, staging, 2)
	assert.Equal(t, []string{"app.example.com"}, staging["web"].Domains)
	assert.Equal(t, int32(3), *staging["web"].Regions[testRegion].Replicas.Max)
}

func TestResolveNullRemovesKeys(t *testing.T) {
	yaml := minimalFile + `    env: { A: "1", B: "2" }
    domains: [api.example.com]
    environments:
      prod:
        domains: null
        env: { B: null, C: "3" }
`
	file, err := Parse([]byte(yaml))
	require.NoError(t, err)

	prod, err := Resolve(file, "prod")
	require.NoError(t, err)
	assert.Nil(t, prod["api"].Domains)
	assert.Equal(t, map[string]string{"A": "1", "C": "3"}, prod["api"].Env)
}

func TestResolveEnabledTrueKeepsService(t *testing.T) {
	file, err := Parse([]byte(minimalFile + "    environments:\n      prod:\n        enabled: true\n"))
	require.NoError(t, err)

	prod, err := Resolve(file, "prod")
	require.NoError(t, err)
	assert.Contains(t, prod, "api")
}

func TestResolveWithoutParsing(t *testing.T) {
	one := int32(1)
	file := &File{
		Version: Version,
		Partial: "api",
		Services: map[string]Service{
			"api": {
				Image: "ghcr.io/acme/api:1",
				Regions: map[string]Region{
					testRegion: {CPU: "100m", Memory: "256Mi", Replicas: &Replicas{Min: &one, Max: &one}},
				},
				Environments: map[string]Override{
					"prod": {Env: map[string]string{"A": "1"}},
				},
			},
		},
	}
	prod, err := Resolve(file, "prod")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"A": "1"}, prod["api"].Env)
	assert.Equal(t, "100m", prod["api"].Regions[testRegion].CPU)
}
