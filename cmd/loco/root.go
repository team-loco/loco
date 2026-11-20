package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/groupcmd"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/internal/session"
	"github.com/team-loco/loco/cmd/loco/config"
	"github.com/team-loco/loco/cmd/loco/infra"
	"github.com/team-loco/loco/cmd/loco/org"
	"github.com/team-loco/loco/cmd/loco/resource"
	"github.com/team-loco/loco/cmd/loco/secret"
	"github.com/team-loco/loco/cmd/loco/token"
	"github.com/team-loco/loco/cmd/loco/workspace"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Env struct {
	Tokens func() (keychain.TokenStore, error)

	versionCheck *versionCheck
}

func NewEnv() Env {
	return Env{Tokens: keychain.ForCurrentUser, versionCheck: &versionCheck{}}
}

func NewRootCmd(env Env) *cobra.Command {
	var startTime time.Time
	root := &cobra.Command{
		Use:   "loco",
		Short: "The CLI for managing loco deployments",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			startTime = time.Now()
			if err := initLogger(cmd); err != nil {
				return fmt.Errorf("failed to initialize logger: %w", err)
			}
			env.versionCheck.start(cmd)
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, _ []string) {
			slog.Info(
				"command finished",
				"command", cmd.Name(),
				"duration", time.Since(startTime),
			)
		},
	}

	root.AddCommand(newLoginCmd(env),
		newLogoutCmd(env),
		newUseCmd(),
		newWhoAmICmd(env),
		newWebCmd(),
		newUpdateCmd(),
		config.BuildConfigCmd(),
	)

	root.AddCommand(infra.BuildDeployCmd())
	root.AddCommand(resource.BuildResourceCmd())
	root.AddCommand(resource.BuildBuildsCmd())
	root.AddCommand(secret.BuildSecretCmd())
	root.AddCommand(org.BuildOrgCmd())
	root.AddCommand(workspace.BuildWorkspaceCmd())
	root.AddCommand(token.BuildTokenCmd())
	root.AddCommand(infra.BuildInfraCmd())
	groupcmd.RejectUnknown(root)
	return root
}

func initLogger(cmd *cobra.Command) error {
	logsDir, err := session.Dir()
	if err != nil {
		return err
	}
	logsPath := filepath.Join(logsDir, "loco.log")

	output := &lumberjack.Logger{
		Filename:   logsPath,
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
		"command", cmd.CommandPath(),
	)
	return nil
}
