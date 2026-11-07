package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/team-loco/loco/cmd/internal/session"
)

func TestCommandsReportUnreadableConfig(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "whoami", args: []string{"whoami", "--host", "http://127.0.0.1:1"}},
		{name: "web", args: []string{"web", "--web-host", "http://127.0.0.1:1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", t.TempDir())
			configPath := filepath.Join(home, ".loco", session.ConfigFileName)
			if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(configPath, []byte("loco_host: [unclosed"), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			var out bytes.Buffer
			root := NewRootCmd(Env{versionCheck: &versionCheck{}})
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tt.args)

			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "decode config "+configPath) {
				t.Fatalf("error = %v, want the config decode failure naming %s", err, configPath)
			}
		})
	}
}
