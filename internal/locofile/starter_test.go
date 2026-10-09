package locofile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSchemaURL = "https://loco.test/schemas/loco.v1.json"

func TestStarterRender(t *testing.T) {
	starter := Starter{
		SchemaURL:   testSchemaURL,
		Name:        "my-app",
		Hostname:    "my-app.onloco.app",
		Port:        8000,
		Region:      testRegion,
		CPU:         "100m",
		Memory:      "256Mi",
		MinReplicas: 1,
		MaxReplicas: 2,
	}
	data, err := starter.Render()
	require.NoError(t, err)

	lines := strings.Split(string(data), "\n")
	assert.Equal(t, "# yaml-language-server: $schema="+testSchemaURL, lines[0])

	file, err := Parse(data)
	require.NoError(t, err)
	assert.Equal(t, "my-app", file.Partial)
	service := file.Services["my-app"]
	assert.Equal(t, "Dockerfile", service.Dockerfile)
	assert.Equal(t, int32(8000), *service.Port)
	assert.Equal(t, []string{"my-app.onloco.app"}, service.Domains)
	region := service.Regions[testRegion]
	assert.Equal(t, "100m", region.CPU)
	assert.Equal(t, "256Mi", region.Memory)
	assert.Equal(t, int32(1), *region.Replicas.Min)
	assert.Equal(t, int32(2), *region.Replicas.Max)
}
