package infra

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestDirectoryServiceName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My App")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	name, err := directoryServiceName()
	if err != nil || name != "my-app" {
		t.Fatalf("directoryServiceName() = %q, %v; want my-app", name, err)
	}

	starter := locofile.Starter{
		SchemaURL: "https://loco.test/schemas/loco.v1.json", Name: name, Hostname: name + ".onloco.app",
		Port: 8000, Region: "us-east", CPU: "100m", Memory: "256Mi", MinReplicas: 1, MaxReplicas: 2,
	}
	data, err := starter.Render()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locofile.Parse(data); err != nil {
		t.Fatalf("starter for %q does not validate: %v", name, err)
	}
}

func TestDirectoryServiceNameRejectsUnusableDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "!!!")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if _, err := directoryServiceName(); !errors.Is(err, errUnsafeDirectoryName) {
		t.Fatalf("directoryServiceName() error = %v, want %v", err, errUnsafeDirectoryName)
	}
}

func TestDomainSuffix(t *testing.T) {
	first := domainSuffix("workspace-a", "web")
	if !isDomainSuffix(first) {
		t.Fatalf("domainSuffix() = %q, want four lowercase letters", first)
	}
	if got := domainSuffix("workspace-a", "web"); got != first {
		t.Fatalf("domainSuffix() = %q, want stable %q", got, first)
	}
	if got := domainSuffix("workspace-b", "web"); got == first {
		t.Fatalf("another workspace returned the same suffix %q", got)
	}
}
