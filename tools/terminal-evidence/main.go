//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

type captureOptions struct {
	out, name, theme, until string
	cols, rows, expectExit  int
	timeout                 time.Duration
	command                 []string
}

const (
	darkTheme  = "dark"
	lightTheme = "light"
	bothThemes = "both"
)

var filenamePrefix = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: terminal-evidence capture [options] -- command [args] | build-device-login [options]")
	}
	switch args[0] {
	case "capture":
		return runCapture(ctx, args[1:])
	case "build-device-login":
		return runBuild(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runCapture(ctx context.Context, args []string) error {
	var opts captureOptions
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.StringVar(&opts.out, "out", "", "directory for PNGs")
	flags.StringVar(&opts.name, "name", "", "filename prefix")
	flags.StringVar(&opts.theme, "theme", bothThemes, "both, dark, or light")
	flags.StringVar(&opts.until, "until", "", "capture when this text appears")
	flags.IntVar(&opts.cols, "cols", 88, "terminal columns")
	flags.IntVar(&opts.rows, "rows", 16, "terminal rows")
	flags.IntVar(&opts.expectExit, "expect-exit", 0, "expected completed command exit code")
	flags.DurationVar(&opts.timeout, "timeout", 15*time.Second, "time limit (e.g. 15s)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	opts.command = flags.Args()
	if err := opts.validate(); err != nil {
		return err
	}
	return captureScreens(ctx, opts, os.Stdout)
}

func (o captureOptions) validate() error {
	if o.out == "" || !filenamePrefix.MatchString(o.name) || len(o.command) == 0 {
		return errors.New("--out, --name (letters, digits, hyphens, underscores), and a command are required")
	}
	if o.cols <= 0 || o.rows <= 0 || o.cols > 65535 || o.rows > 65535 || o.timeout <= 0 {
		return errors.New("terminal dimensions must fit a PTY and timeout must be positive")
	}
	if o.theme != bothThemes && o.theme != darkTheme && o.theme != lightTheme {
		return fmt.Errorf("unknown terminal theme %q", o.theme)
	}
	return nil
}

func captureScreens(ctx context.Context, opts captureOptions, out io.Writer) error {
	themes := []string{opts.theme}
	if opts.theme == bothThemes {
		themes = []string{darkTheme, lightTheme}
	}
	if err := os.MkdirAll(opts.out, 0o700); err != nil {
		return err
	}
	for _, theme := range themes {
		// A failed retry must not leave stale evidence.
		if err := os.Remove(outputPath(opts, theme)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, theme := range themes {
		scheme := themeFor(theme)
		screen, err := capture(ctx, opts, scheme)
		if err != nil {
			return fmt.Errorf("%s capture: %w", theme, err)
		}
		path := outputPath(opts, theme)
		if err := savePNG(path, screen, scheme); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, path); err != nil {
			return err
		}
	}
	return nil
}

func outputPath(opts captureOptions, theme string) string {
	return filepath.Join(opts.out, opts.name+"-"+theme+".png")
}
