package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestRegistryPath(t *testing.T) {
	path, err := registryPath(testRegistryHost+"/", testRegistryHost+"/builds/ws-1/app")
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	if path != "builds/ws-1/app" {
		t.Fatalf("path = %q, want builds/ws-1/app", path)
	}
	for _, repository := range []string{"other.registry/builds/ws-1/app", testRegistryHost, testRegistryHost + "/"} {
		if _, foreignErr := registryPath(testRegistryHost, repository); !errors.Is(foreignErr, errForeignRepository) {
			t.Errorf("registry path of %q = %v, want %v", repository, foreignErr, errForeignRepository)
		}
	}
}

func TestParseRepositoryPath(t *testing.T) {
	workspaceID := uuid.New()
	resourceID := uuid.New()
	repository := imageRepository(testRegistryHost, "/builds/", workspaceID, resourceID)
	path, err := registryPath(testRegistryHost, repository)
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	parsed, ok := parseRepositoryPath("/builds/", path)
	if !ok || parsed != resourceID {
		t.Fatalf("parse %q = %s, %v, want %s", path, parsed, ok, resourceID)
	}

	unprefixed := imageRepository(testRegistryHost, "", workspaceID, resourceID)
	unprefixedPath, err := registryPath(testRegistryHost, unprefixed)
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	if parsed, ok = parseRepositoryPath("", unprefixedPath); !ok || parsed != resourceID {
		t.Fatalf("parse %q without a prefix = %s, %v, want %s", unprefixedPath, parsed, ok, resourceID)
	}

	workspacePath := "ws-" + workspaceID.String()
	resourcePath := resourceID.String()
	rejected := []string{
		"other/" + workspacePath + "/" + resourcePath,
		"builds/" + resourcePath,
		"builds/" + workspacePath + "/" + resourcePath + "/extra",
		"builds/" + workspaceID.String() + "/" + resourcePath,
		"builds/ws-nope/" + resourcePath,
		"builds/" + workspacePath + "/app",
	}
	for _, candidate := range rejected {
		if id, matched := parseRepositoryPath("builds", candidate); matched {
			t.Errorf("parse %q = %s, want no match", candidate, id)
		}
	}
}
