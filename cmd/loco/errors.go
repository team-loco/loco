package loco

import (
	"errors"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
)

var (
	// Flag parsing errors
	ErrFlagParsing = errors.New("failed to parse command flag")

	// Configuration errors
	ErrConfigLoad     = errors.New("failed to load configuration")
	ErrConfigValidate = errors.New("configuration validation failed")
	ErrConfigPath     = errors.New("invalid configuration path")

	// Authentication errors
	ErrTokenExpired  = errors.New("authentication token has expired")
	ErrTokenInvalid  = errors.New("invalid authentication token")
	ErrLoginRequired = cmdutil.ErrLoginRequired
	ErrAuthFailed    = errors.New("authentication failed")

	// Docker errors
	ErrDockerClient   = errors.New("failed to create Docker client")
	ErrDockerBuild    = errors.New("docker build failed")
	ErrDockerPush     = errors.New("docker push failed")
	ErrDockerValidate = errors.New("docker image validation failed")

	// Network/API errors
	ErrAPIRequest   = errors.New("API request failed")
	ErrNetworkError = errors.New("network error occurred")

	// File system errors
	ErrFileAccess = errors.New("file access error")

	// Command execution errors
	ErrCommandFailed = errors.New("command execution failed")

	// Validation errors
	ErrValidation = errors.New("validation failed")
)
