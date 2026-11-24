package host

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParseURL(t *testing.T) {
	const generic = "set --api to an http:// or https:// URL"
	tests := []struct{ value, want string }{
		{"api.loco.build", "set --api to a URL such as https://api.loco.build"},
		{"api.loco.build:8443", "set --api to a URL such as https://api.loco.build:8443"},
		{"localhost", "set --api to a URL such as https://localhost"},
		{"localhost:8080", "set --api to a URL such as https://localhost:8080"},
		{"127.0.0.1", "set --api to a URL such as https://127.0.0.1"},
		{"127.0.0.1:8080", "set --api to a URL such as https://127.0.0.1:8080"},
		{"[::1]:8080", "set --api to a URL such as https://[::1]:8080"},
		{"http://localhost:8080", ""},
		{"http://127.0.0.1", ""},
		{"https://api.loco.build", ""},
		{"https://[::1]:8080", ""},
		{"ftp://x", generic},
		{"https://u:p@h", generic},
		{"https://h?x=1", generic},
		{"https://h?", generic},
		{"https://h#f", generic},
		{"https://", generic},
		{"https:", generic},
		{"//host", generic},
		{"http:/h", generic},
		{"::1", generic},
		{"", "set --api to the server's URL, such as https://api.loco.build"},
	}
	for _, test := range tests {
		u, err := ParseURL("--api", test.value)
		if test.want == "" {
			if err != nil || u.String() != test.value {
				t.Errorf("ParseURL(%q) = %v, %v, want the URL", test.value, u, err)
			}
			continue
		}
		if err == nil || !strings.HasPrefix(err.Error(), test.want) {
			t.Errorf("ParseURL(%q) error = %v, want prefix %q", test.value, err, test.want)
		}
	}
}
