package cmdutil

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/session"
)

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

func TestResolveOrgAndWorkspaceReportUnreadableConfig(t *testing.T) {
	t.Setenv("LOCO_ORG", "")
	t.Setenv("LOCO_WORKSPACE", "")
	loadErr := errors.New("decode failure")
	loadConfig := func() (*session.SessionConfig, error) { return nil, loadErr }

	cmd := &cobra.Command{}
	cmd.Flags().String("org", "", "")
	cmd.Flags().String("workspace", "", "")

	if _, err := ResolveOrg(cmd, loadConfig); !errors.Is(err, loadErr) {
		t.Fatalf("ResolveOrg error = %v, want it to wrap %v", err, loadErr)
	}
	if _, err := ResolveWorkspace(cmd, loadConfig); !errors.Is(err, loadErr) {
		t.Fatalf("ResolveWorkspace error = %v, want it to wrap %v", err, loadErr)
	}
}
