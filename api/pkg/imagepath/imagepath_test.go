package imagepath

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

const testRegistryHost = "registry.loco.test"

func TestRepository(t *testing.T) {
	workspaceID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	resourceID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	suffix := "/ws-" + workspaceID.String() + "/" + resourceID.String()

	tests := []struct {
		host   string
		prefix string
		want   string
	}{
		{host: testRegistryHost, prefix: "builds", want: testRegistryHost + "/builds" + suffix},
		{host: testRegistryHost + "/", prefix: "/team/builds/", want: testRegistryHost + "/team/builds" + suffix},
		{host: testRegistryHost, want: testRegistryHost + suffix},
	}

	for _, tt := range tests {
		if got := Repository(tt.host, tt.prefix, workspaceID, resourceID); got != tt.want {
			t.Errorf("Repository(%q, %q) = %q, want %q", tt.host, tt.prefix, got, tt.want)
		}
	}
}

func TestPath(t *testing.T) {
	path, err := Path(testRegistryHost+"/", testRegistryHost+"/builds/ws-1/app")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if path != "builds/ws-1/app" {
		t.Fatalf("path = %q, want builds/ws-1/app", path)
	}
	for _, repository := range []string{"other.registry/builds/ws-1/app", testRegistryHost, testRegistryHost + "/"} {
		if _, foreignErr := Path(testRegistryHost, repository); !errors.Is(foreignErr, ErrForeignRepository) {
			t.Errorf("path of %q = %v, want %v", repository, foreignErr, ErrForeignRepository)
		}
	}
}

func TestParseResourceRoundTripsRepository(t *testing.T) {
	workspaceID := uuid.New()
	resourceID := uuid.New()
	for _, prefix := range []string{"/builds/", "builds", ""} {
		repository := Repository(testRegistryHost, prefix, workspaceID, resourceID)
		path, err := Path(testRegistryHost, repository)
		if err != nil {
			t.Fatalf("path of %q: %v", repository, err)
		}
		parsed, ok := ParseResource(prefix, path)
		if !ok || parsed != resourceID {
			t.Errorf("parse %q with prefix %q = %s, %v, want %s", path, prefix, parsed, ok, resourceID)
		}
	}

	workspacePath := WorkspaceDirPrefix + workspaceID.String()
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
		if id, matched := ParseResource("builds", candidate); matched {
			t.Errorf("parse %q = %s, want no match", candidate, id)
		}
	}
}
