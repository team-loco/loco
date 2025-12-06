package loco

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

var webCmd = &cobra.Command{
	Use:   "web [dashboard|logs|docs|account]",
	Short: "Open loco pages in your browser",
	Long:  "Open loco pages in your browser. Defaults to home if no argument provided.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return webCmdFunc(cmd, args)
	},
}

func webCmdFunc(cmd *cobra.Command, args []string) error {
	host, err := getHost(cmd)
	if err != nil {
		return err
	}

	page := ""
	if len(args) > 0 {
		page = args[0]
	}

	var path string
	switch page {
	case "dashboard":
		path = "/dashboard"
	case "logs":
		path = "/logs"
	case "docs":
		path = "/docs"
	case "account":
		path = "/profile"
	case "":
		path = "/"
	default:
		return fmt.Errorf("invalid page: %s. Valid options are: dashboard, logs, docs, account", page)
	}

	url := host + path

	displayPage := page
	if displayPage == "" {
		displayPage = "home"
	}

	slog.Debug("opening url in browser", "url", url, "page", displayPage)

	if err := openBrowser(url); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	fmt.Printf("Opening %s in your browser...\n", displayPage)

	return nil
}

func openBrowser(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to execute browser command: %w", err)
	}

	return nil
}

func init() {
	webCmd.Flags().String("host", "", "Set the host URL")
}
