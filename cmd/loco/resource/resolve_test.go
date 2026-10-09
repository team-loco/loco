package resource

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/session"
)

func TestConfigToResourceSpecMarksMetadataRegionPrimary(t *testing.T) {
	const primary = "us-west-1"
	resources := config.Resources{CPU: "100m", Memory: "256Mi", ReplicasMin: 1, ReplicasMax: 1}
	cfg := &config.LocoConfig{
		Metadata: config.Metadata{Region: primary},
		RegionConfig: map[string]config.Resources{
			"eu-west-1": resources,
			"us-east-1": resources,
			primary:     resources,
		},
	}

	for range 20 {
		spec, err := configToResourceSpec(cfg, "v1")
		if err != nil {
			t.Fatalf("configToResourceSpec: %v", err)
		}
		for name, target := range spec.GetService().GetRegions() {
			want := name == primary
			if target.GetPrimary() != want {
				t.Fatalf("region %s: primary = %v, want %v", name, target.GetPrimary(), want)
			}
		}
	}
}

func TestResolveWorkspaceIDIgnoresCachedWorkspaceOfAnotherOrg(t *testing.T) {
	loadConfig := func() (*session.SessionConfig, error) {
		cfg := session.NewSessionConfig()
		cfg.Scopes[session.DefaultScope] = &session.Scope{
			Organization: session.SimpleOrg{ID: "org-a-id", Name: "org-a"},
			Workspace:    session.SimpleWorkspace{ID: "org-a-default-id", Name: "default"},
		}
		return cfg, nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("org", "", "")
	cmd.Flags().String("workspace", "", "")
	if err := cmd.Flags().Set("org", "org-b"); err != nil {
		t.Fatalf("set org flag: %v", err)
	}

	id, err := ResolveWorkspaceID(context.Background(), cmd, loadConfig, nil)
	if id == "org-a-default-id" {
		t.Fatalf("resolved org-b's default workspace to org-a's cached workspace ID")
	}
	if err == nil {
		t.Fatalf("expected an error resolving org-b without an API client, got id %q", id)
	}
}
