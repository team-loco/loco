package groupcmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/groupcmd"
)

const groupName = "group"

func newTree() *cobra.Command {
	root := &cobra.Command{Use: "tool"}
	group := &cobra.Command{Use: groupName}
	group.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
	nested := &cobra.Command{Use: "nested"}
	nested.AddCommand(&cobra.Command{Use: "leaf", Run: func(*cobra.Command, []string) {}})
	group.AddCommand(nested)
	root.AddCommand(group)
	groupcmd.RejectUnknown(root)
	return root
}

func run(args ...string) (string, error) {
	root := newTree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestUnknownSubcommandFails(t *testing.T) {
	for _, args := range [][]string{{groupName, "lsit"}, {groupName, "nested", "lef"}} {
		if _, err := run(args...); err == nil {
			t.Errorf("%v: want unknown command error", args)
		}
	}
}

func TestGroupWithoutArgsPrintsHelp(t *testing.T) {
	out, err := run(groupName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Available Commands") {
		t.Errorf("help not printed: %q", out)
	}
}

func TestKnownSubcommandRuns(t *testing.T) {
	if _, err := run(groupName, "nested", "leaf"); err != nil {
		t.Fatal(err)
	}
}
