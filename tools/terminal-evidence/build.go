//go:build darwin || linux

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed device_login_test.go.txt
var deviceLoginFixture []byte

func runBuild(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("build-device-login", flag.ContinueOnError)
	checkout := flags.String("checkout", "", "checkout containing the loco CLI tests")
	output := flags.String("out", "", "output test binary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *checkout == "" || *output == "" || flags.NArg() != 0 {
		return errors.New("--checkout and --out are required")
	}
	return buildDeviceLogin(ctx, *checkout, *output)
}

func buildDeviceLogin(ctx context.Context, checkout, output string) (resultErr error) {
	checkout, err := filepath.Abs(checkout)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "loco-terminal-fixture-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(scratch)) }()
	fixture := filepath.Join(scratch, "fixture.go")
	if err = os.WriteFile(fixture, deviceLoginFixture, 0o600); err != nil {
		return err
	}
	virtual := filepath.Join(checkout, "cmd", "loco", "terminal_evidence_test.go")
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {virtual: fixture}})
	if err != nil {
		return err
	}
	mapping := filepath.Join(scratch, "overlay.json")
	if err = os.WriteFile(mapping, overlay, 0o600); err != nil {
		return err
	}
	whichGo := exec.CommandContext(ctx, "mise", "which", "go")
	whichGo.Dir = checkout
	goPath, err := whichGo.Output()
	if err != nil {
		return fmt.Errorf("find the checkout's Go tool: %w", err)
	}
	cmd := exec.CommandContext(
		ctx,
		strings.TrimSpace(string(goPath)),
		"test",
		"-overlay",
		mapping,
		"-c",
		"-o",
		output,
		"./loco",
	)
	cmd.Dir = filepath.Join(checkout, "cmd")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
