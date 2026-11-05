package cmdutil

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func promptCommand(t *testing.T, input string, interactive bool, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	previous := isInteractive
	isInteractive = func() bool { return interactive }
	t.Cleanup(func() { isInteractive = previous })

	cmd := &cobra.Command{}
	cmd.Flags().BoolP("yes", "y", false, "")
	cmd.SetIn(strings.NewReader(input))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd, &out
}

func TestConfirm(t *testing.T) {
	tests := map[string]struct {
		input       string
		interactive bool
		args        []string
		want        bool
		wantErr     error
		wantOut     string
	}{
		"yes answer":         {input: "y", interactive: true, want: true},
		"no answer":          {input: "n", interactive: true, wantOut: "Aborted."},
		"yes flag":           {interactive: true, args: []string{"--yes"}, want: true},
		"yes flag no prompt": {args: []string{"-y"}, want: true},
		"not a terminal":     {input: "y", wantErr: ErrNotInteractive},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cmd, out := promptCommand(t, tt.input, tt.interactive, tt.args...)
			got, err := Confirm(cmd, "Proceed?")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Confirm() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Confirm() = %v, want %v", got, tt.want)
			}
			if tt.wantOut != "" && !strings.Contains(out.String(), tt.wantOut) {
				t.Fatalf("output = %q, want it to contain %q", out.String(), tt.wantOut)
			}
			if tt.wantOut == "" && strings.Contains(out.String(), "Aborted.") {
				t.Fatalf("output = %q, want no abort message", out.String())
			}
			if tt.wantErr != nil && !strings.Contains(err.Error(), "--yes") {
				t.Fatalf("error = %q, want it to name --yes", err)
			}
		})
	}
}

func TestAskStringRefusesWithoutTerminal(t *testing.T) {
	cmd, _ := promptCommand(t, "web\n", false)
	if _, err := AskString(cmd, "Name: "); !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("AskString() error = %v, want %v", err, ErrNotInteractive)
	}
}

func TestAskString(t *testing.T) {
	cmd, _ := promptCommand(t, "web\r", true)
	got, err := AskString(cmd, "Name: ")
	if err != nil {
		t.Fatalf("AskString() error = %v", err)
	}
	if got != "web" {
		t.Fatalf("AskString() = %q, want web", got)
	}
}
