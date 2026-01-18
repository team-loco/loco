package loco

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/org"
	"github.com/team-loco/loco/cmd/loco/service"
	"github.com/team-loco/loco/cmd/loco/token"
	"github.com/team-loco/loco/cmd/loco/workspace"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	logPath   string
	startTime time.Time
)

var RootCmd = &cobra.Command{
	Use:   "loco",
	Short: "The CLI for managing loco deployments",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		startTime = time.Now()
		if err := initLogger(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
			os.Exit(1)
		}
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		slog.Info(
			"command finished",
			"command", cmd.Name(),
			"duration", time.Since(startTime),
		)
	},
}

func initLogger(cmd *cobra.Command) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	logsDir := filepath.Join(home, ".loco")
	logPath = filepath.Join(logsDir, "loco.log")

	output := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    2, // megabytes
		MaxBackups: 0,
		MaxAge:     30, // days
		Compress:   false,
	}

	logger := slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)
	slog.Info(
		"new run",
		"version", cmd.Root().Version,
		"args", os.Args,
	)
	return nil
}

func init() {
	// Root-level commands
	RootCmd.AddCommand(loginCmd, useCmd, buildWhoAmICmd(), initCmd, validateCmd, webCmd)

	// Resource-based commands
	RootCmd.AddCommand(service.BuildServiceCmd())
	RootCmd.AddCommand(org.BuildOrgCmd())
	RootCmd.AddCommand(workspace.BuildWorkspaceCmd())
	RootCmd.AddCommand(token.BuildTokenCmd())
}
