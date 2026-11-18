package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

const (
	ConfigFileName = "config.yaml"

	configFileMode = 0o600
)

// SessionConfig represents the CLI's local state and authentication configuration.
// It is persisted to ~/.loco/config.yaml.
type SessionConfig struct {
	LocoHost     string          `json:"locoHost,omitempty"`
	WebHost      string          `json:"webHost,omitempty"`
	Organization SimpleOrg       `json:"organization"`
	Workspace    SimpleWorkspace `json:"workspace"`
}

// SimpleOrg represents an organization with its ID and name.
type SimpleOrg struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// SimpleWorkspace represents a workspace with its ID and name.
type SimpleWorkspace struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	locoDir := filepath.Join(home, ".loco")
	return locoDir, nil
}

func GetConfigPath() (string, error) {
	locoDir, err := Dir()
	if err != nil {
		return "", err
	}

	if err = os.MkdirAll(locoDir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create .loco directory: %w", err)
	}

	return filepath.Join(locoDir, ConfigFileName), nil
}

// Load reads the SessionConfig from ~/.loco/config.yaml. Returns a new config if the file doesn't exist.
func Load() (*SessionConfig, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return &SessionConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg SessionConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", configPath, err)
	}

	return &cfg, nil
}

// Save persists the SessionConfig to ~/.loco/config.yaml.
func (c *SessionConfig) Save() error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}

	if err := os.WriteFile(configPath, data, configFileMode); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// SetScope saves org and wks as the organization and workspace commands use by default.
func (c *SessionConfig) SetScope(org SimpleOrg, wks SimpleWorkspace) error {
	c.Organization = org
	c.Workspace = wks
	return c.Save()
}
