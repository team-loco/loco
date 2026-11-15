package locofile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveMergesFilesByPath(t *testing.T) {
	yaml := minimalFile + `    files:
      /otel-config.yml: |
        file_format: "1.0"
      /etc/app/base.yml: "a: 1"
    environments:
      prod:
        files:
          /etc/app/base.yml: null
          /etc/app/prod.yml: "b: 2"
`
	file, err := Parse([]byte(yaml))
	require.NoError(t, err)
	assert.Equal(t, "file_format: \"1.0\"\n", file.Services["api"].Files["/otel-config.yml"])

	prod, err := Resolve(file, "prod")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"/otel-config.yml":  "file_format: \"1.0\"\n",
		"/etc/app/prod.yml": "b: 2",
	}, prod["api"].Files)
}
