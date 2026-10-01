package loco

import (
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/config"
	"github.com/team-loco/loco/cmd/loco/org"
	"github.com/team-loco/loco/cmd/loco/resource"
	"github.com/team-loco/loco/cmd/loco/token"
	"github.com/team-loco/loco/cmd/loco/workspace"
	"github.com/team-loco/loco/internal/keychain"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Env struct {
	Tokens keychain.TokenStore
}

func NewEnv() (Env, error) {
	currentUser, err := user.Current()
	if err != nil {
		return Env{}, fmt.Errorf("failed to get current user: %w", err)
	}
	tokens, err := keychain.NewStore(currentUser.Name)
	if err != nil {
		return Env{}, err
	}
	return Env{Tokens: tokens}, nil
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

	root.AddCommand(newLoginCmd(),
		newLogoutCmd(env),
		newUseCmd(),
		newWhoAmICmd(env),
		newInitCmd(),
		newValidateCmd(),
		newWebCmd(),
		config.BuildConfigCmd(),
	)

	root.AddCommand(resource.BuildResourceCmd())
	root.AddCommand(org.BuildOrgCmd())
	root.AddCommand(workspace.BuildWorkspaceCmd())
	root.AddCommand(token.BuildTokenCmd())
	return root
}

func initLogger(cmd *cobra.Command) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	output := &lumberjack.Logger{
		Filename:   filepath.Join(home, ".loco", "loco.log"),
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
