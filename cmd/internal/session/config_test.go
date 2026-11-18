package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveWritesYAMLThatLoadReadsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &SessionConfig{LocoHost: "https://api.loco.test"}
	org := SimpleOrg{Name: "acme", ID: "1"}
	wks := SimpleWorkspace{Name: "production", ID: "2"}
	require.NoError(t, cfg.SetScope(org, wks))

	configPath := filepath.Join(home, ".loco", "config.yaml")
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.YAMLEq(t, `
locoHost: https://api.loco.test
organization:
  name: acme
  id: "1"
workspace:
  name: production
  id: "2"
`, string(data))

	loaded, err := Load()
	require.NoError(t, err)
	assert.Equal(t, cfg, loaded)
}

func TestLoadWithoutConfigFileReturnsAnEmptyConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, &SessionConfig{}, cfg)
}
