package locofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOverrideImagePreservesOtherEnvironmentsAndNulls(t *testing.T) {
	original := []byte(`version: 1
partial: app
services:
  api:
    dockerfile: Dockerfile
    context: source
    health: {path: /health}
    regions:
      us-east-1: {cpu: 100m, memory: 128Mi, replicas: {min: 1, max: 1}}
    environments:
      production:
        dockerfile: Dockerfile.production
        health: null
      staging:
        context: staging
      disabled:
        enabled: false
  worker:
    image: example.com/worker:v1
    regions:
      us-east-1: {cpu: 100m, memory: 128Mi, replicas: {min: 1, max: 1}}
`)
	file, err := Parse(original)
	require.NoError(t, err)
	encoded, err := OverrideImage(file, "api", "production", "example.com/api@sha256:abc")
	require.NoError(t, err)
	overridden, err := Parse(encoded)
	require.NoError(t, err)
	production, err := Resolve(overridden, "production")
	require.NoError(t, err)
	require.Equal(t, "example.com/api@sha256:abc", production["api"].Image)
	require.Empty(t, production["api"].Dockerfile)
	require.Empty(t, production["api"].Context)
	require.Nil(t, production["api"].Health)
	for _, environment := range []string{"staging", "disabled"} {
		before, resolveErr := Resolve(file, environment)
		require.NoError(t, resolveErr)
		after, resolveErr := Resolve(overridden, environment)
		require.NoError(t, resolveErr)
		require.Equal(t, before, after)
	}
	unchanged, err := Resolve(file, "production")
	require.NoError(t, err)
	require.Equal(t, "Dockerfile.production", unchanged["api"].Dockerfile)
	require.Equal(t, production["worker"], unchanged["worker"])
	_, err = OverrideImage(file, "missing", "production", "example.com/api:v1")
	require.ErrorIs(t, err, ErrServiceNotFound)
}
