package groupcmd_test

import (
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/groupcmd"
)

func TestUnknownSubcommandFails(t *testing.T) {
	const groupName = "group"
	for _, args := range [][]string{{groupName, "lsit"}, {groupName, "nested", "lef"}} {
		root := &cobra.Command{Use: "tool"}
		group := &cobra.Command{Use: groupName}
		group.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
		nested := &cobra.Command{Use: "nested"}
		nested.AddCommand(&cobra.Command{Use: "leaf", Run: func(*cobra.Command, []string) {}})
		group.AddCommand(nested)
		root.AddCommand(group)
		groupcmd.RejectUnknown(root)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("%v: want unknown command error", args)
		}
	}
}
