package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const (
	suiteName      = "installation-e2e"
	cleanupTimeout = 2 * time.Minute
	hostTimeout    = 75 * time.Minute
)

var errSuiteTimeout = errors.New("installation suite timeout must be positive")

func main() {
	if checkErr := run(); checkErr != nil {
		fmt.Fprintln(os.Stderr, checkErr)
		os.Exit(1)
	}
}

func run() error {
	if name := filepath.Base(os.Args[0]); name == helmExecutable || name == kubectlExecutable {
		return auditExecutable()
	}
	options := flag.NewFlagSet(suiteName, flag.ContinueOnError)
	options.SetOutput(io.Discard)
	mail := options.Bool("mail-sink", false, "Serve the external fixture email transport")
	client := options.Bool("client", false, "Run operator acceptance inside the fixture pod")
	keep := options.Bool("no-teardown", false, "Keep this suite's kind cluster for inspection")
	skip := options.Bool("skip-build", false, "Reuse this suite's built images and binaries")
	timeout := options.Duration("timeout", hostTimeout, "Maximum duration of host setup and acceptance")
	if checkErr := options.Parse(os.Args[1:]); checkErr != nil {
		return errors.New("invalid installation suite options")
	}
	if options.NArg() != 0 {
		return errors.New("installation suite accepts no test filters")
	}
	if *timeout <= 0 {
		return errSuiteTimeout
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *mail {
		return mailSink(ctx)
	}
	if *client {
		return clientAcceptance(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, checkErr := os.Stat(filepath.Join(root, "mise.toml")); checkErr != nil {
		return errors.New("run the installation suite from the repository root")
	}
	fixture, err := newFixture(root, *skip)
	if err != nil {
		return err
	}
	return execute(ctx, fixture.acceptance, func(ctx context.Context) error {
		return fixture.cleanup(ctx, *keep)
	})
}

func execute(ctx context.Context, acceptance, cleanup func(context.Context) error) (err error) {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		err = errors.Join(err, cleanup(cleanupCtx))
	}()
	return acceptance(ctx)
}

func command(ctx context.Context, dir string, input []byte, program string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var out []byte
	var err error
	if audit, ok := ctx.Value(auditKey{}).(*leakAudit); ok && (program == "locoadm" || program == "loco") {
		var stdout, stderr boundedOutput
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err = cmd.Run()
		audit.capture("CLI output (stdout)", stdout.Bytes())
		audit.capture("CLI output (stderr)", stderr.Bytes())
		out = append(stdout.Bytes(), stderr.Bytes()...)
		if stdout.exceeded || stderr.exceeded {
			audit.mu.Lock()
			audit.exceeded = true
			audit.mu.Unlock()
		}
	} else {
		out, err = cmd.CombinedOutput()
	}
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", program, err)
	}
	return out, nil
}

func stopProcess(cmd *exec.Cmd) error {
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop fixture subprocess: %w", err)
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != -1) {
		return fmt.Errorf("wait fixture subprocess: %w", err)
	}
	return nil
}
