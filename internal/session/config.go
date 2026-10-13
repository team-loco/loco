package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

var errEmptyScopeName = errors.New("scope name cannot be empty")

const (
	ConfigFileName = "config.yaml"
	DefaultScope   = "default"

	configFileMode = 0o600
)

// SessionConfig represents the CLI's local state and authentication configuration.
// It is persisted to ~/.loco/config.yaml.
type SessionConfig struct {
	LocoHost         string            `json:"locoHost,omitempty"`
	WebHost          string            `json:"webHost,omitempty"`
	DefaultAppDomain string            `json:"defaultAppDomain,omitempty"`
	Scopes           map[string]*Scope `json:"scopes"`
	CurrentScope     string            `json:"currentScope"`
}

// Scope represents a CLI context with an organization and workspace.
type Scope struct {
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

// NewSessionConfig creates a new SessionConfig with the default scope.
func NewSessionConfig() *SessionConfig {
	return &SessionConfig{
		Scopes:       make(map[string]*Scope),
		CurrentScope: DefaultScope,
	}
}

// Load reads the SessionConfig from ~/.loco/config.yaml. Returns a new config if the file doesn't exist.
func Load() (*SessionConfig, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return NewSessionConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg SessionConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	if cfg.CurrentScope == "" {
		cfg.CurrentScope = DefaultScope
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

// GetScope returns the current scope or an error if it's not found.
func (c *SessionConfig) GetScope() (*Scope, error) {
	ctx, ok := c.Scopes[c.CurrentScope]
	if !ok {
		return nil, fmt.Errorf("current scope %q not found", c.CurrentScope)
	}
	return ctx, nil
}

// SetScope creates or updates a scope with the given organization and workspace, sets it as current,
// and persists the config.
func (c *SessionConfig) SetScope(scopeName string, org SimpleOrg, wks SimpleWorkspace) error {
	if scopeName == "" {
		return errEmptyScopeName
	}

	if c.Scopes == nil {
		c.Scopes = make(map[string]*Scope)
	}

	ctx, exists := c.Scopes[scopeName]
	if !exists {
		ctx = &Scope{}
		c.Scopes[scopeName] = ctx
	}

	ctx.Organization = org
	ctx.Workspace = wks
	c.CurrentScope = scopeName

	return c.Save()
}

// SetDefaultScope is a convenience method to set the default scope.
func (c *SessionConfig) SetDefaultScope(org SimpleOrg, wks SimpleWorkspace) error {
	return c.SetScope(DefaultScope, org, wks)
}
