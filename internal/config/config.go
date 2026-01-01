package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	ConfigFileName = "config.toml"
	DefaultScope   = "default"
)

// SessionConfig represents the CLI's local state and authentication configuration.
// It is persisted to ~/.loco/config.toml.
type SessionConfig struct {
	Scopes       map[string]*Scope `toml:"scopes"`
	CurrentScope string            `toml:"currentScope"`
}

// Scope represents a CLI context with an organization and workspace.
type Scope struct {
	Organization SimpleOrg       `toml:"organization"`
	Workspace    SimpleWorkspace `toml:"workspace"`
}

// SimpleOrg represents an organization with its ID and name.
type SimpleOrg struct {
	Name string `toml:"name"`
	ID   int64  `toml:"id"`
}

// SimpleWorkspace represents a workspace with its ID and name.
type SimpleWorkspace struct {
	Name string `toml:"name"`
	ID   int64  `toml:"id"`
}

func GetConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	locoDir := filepath.Join(home, ".loco")
	if err := os.MkdirAll(locoDir, 0o755); err != nil {
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

// Load reads the SessionConfig from ~/.loco/config.toml. Returns a new config if the file doesn't exist.
func Load() (*SessionConfig, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	var cfg SessionConfig
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return NewSessionConfig(), nil
	}

	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	if cfg.CurrentScope == "" {
		cfg.CurrentScope = DefaultScope
	}

	return &cfg, nil
}

// Save persists the SessionConfig to ~/.loco/config.toml.
func (c *SessionConfig) Save() error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	f, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(c); err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
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

// SetScope creates or updates a scope with the given organization and workspace, sets it as current, and persists the config.
func (c *SessionConfig) SetScope(scopeName string, org SimpleOrg, wks SimpleWorkspace) error {
	if scopeName == "" {
		return fmt.Errorf("scope name cannot be empty")
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
