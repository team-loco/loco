// Package imagepath encodes and decodes the registry repository of a resource's build images.
package imagepath

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	// WorkspaceDirPrefix starts the workspace segment of a repository path.
	WorkspaceDirPrefix = "ws-"
	pathParts          = 2
)

var ErrForeignRepository = errors.New("image repository is not on the configured registry host")

// Repository is the full image repository of a resource: host, optional prefix, workspace and resource.
func Repository(registryHost, registryPrefix string, workspaceID, resourceID uuid.UUID) string {
	host := strings.TrimSuffix(registryHost, "/")
	return host + "/" + WorkspacePrefix(registryPrefix, workspaceID) + resourceID.String()
}

// WorkspacePrefix is the registry path, with a trailing slash, that holds every repository of a workspace.
func WorkspacePrefix(registryPrefix string, workspaceID uuid.UUID) string {
	workspacePath := WorkspaceDirPrefix + workspaceID.String() + "/"
	prefix := strings.Trim(registryPrefix, "/")
	if prefix == "" {
		return workspacePath
	}
	return prefix + "/" + workspacePath
}

// Path strips the registry host from an image repository, giving the path registry APIs take.
func Path(registryHost, imageRepository string) (string, error) {
	host := strings.TrimSuffix(registryHost, "/")
	path, ok := strings.CutPrefix(imageRepository, host+"/")
	if !ok || path == "" {
		return "", fmt.Errorf("%w: %q", ErrForeignRepository, imageRepository)
	}
	return path, nil
}

// ParseResource returns the resource of a registry path laid out by Repository, or false for any other path.
func ParseResource(registryPrefix, path string) (uuid.UUID, bool) {
	rest := path
	prefix := strings.Trim(registryPrefix, "/")
	if prefix != "" {
		var ok bool
		rest, ok = strings.CutPrefix(path, prefix+"/")
		if !ok {
			return uuid.UUID{}, false
		}
	}
	parts := strings.Split(rest, "/")
	if len(parts) != pathParts {
		return uuid.UUID{}, false
	}
	rawWorkspaceID, ok := strings.CutPrefix(parts[0], WorkspaceDirPrefix)
	if !ok {
		return uuid.UUID{}, false
	}
	if _, err := uuid.Parse(rawWorkspaceID); err != nil {
		return uuid.UUID{}, false
	}
	resourceID, err := uuid.Parse(parts[1])
	if err != nil {
		return uuid.UUID{}, false
	}
	return resourceID, true
}
