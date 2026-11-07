package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/session"
)

func corruptConfigHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCO_HOST", "")
	t.Setenv("LOCO_WEB_HOST", "")
	locoDir := filepath.Join(home, ".loco")
	if err := os.MkdirAll(locoDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(locoDir, session.ConfigFileName)
	if err := os.WriteFile(configPath, []byte("loco_host: [unclosed"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestGetHostReportsUnreadableConfig(t *testing.T) {
	corruptConfigHome(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("host", "", "")

	host, err := GetHost(cmd)
	if err == nil {
		t.Fatalf("expected an error for a corrupt config, got host %q", host)
	}
}

func TestGetWebHostReportsUnreadableConfig(t *testing.T) {
	corruptConfigHome(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("web-host", "", "")

	host, err := GetWebHost(cmd)
	if err == nil {
		t.Fatalf("expected an error for a corrupt config, got host %q", host)
	}
}
