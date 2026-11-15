package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func command(ctx context.Context, input []byte, environment []string, program string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), environment...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %v: %w", program, args, err)
	}
	return output.Bytes(), nil
}

func (cfg config) kubeconfig() string { return filepath.Join(cfg.home(), ".kube", "config") }
func (cfg config) cliEnv() []string {
	return []string{"HOME=" + cfg.home(), "LOCO_CREDENTIAL_STORE=file", "KUBECONFIG=" + cfg.kubeconfig()}
}
func (cfg config) kubectl(ctx context.Context, args ...string) ([]byte, error) {
	return command(
		ctx,
		nil,
		nil,
		"kubectl",
		append([]string{"--kubeconfig", cfg.kubeconfig(), "--context", "kind-" + cfg.Cluster}, args...)...)
}
func (cfg config) apply(ctx context.Context, data []byte) error {
	_, err := command(
		ctx,
		data,
		nil,
		"kubectl",
		"--kubeconfig",
		cfg.kubeconfig(),
		"--context",
		"kind-"+cfg.Cluster,
		"apply",
		"-f",
		"-",
	)
	return err
}
func (cfg config) compose(ctx context.Context, args ...string) error {
	_, err := command(
		ctx,
		nil,
		nil,
		"docker",
		append([]string{"compose", "--project-name", cfg.Project, "--file", "dev/compose.local.yaml"}, args...)...)
	return err
}
func (cfg config) locoadm(ctx context.Context, args ...string) ([]byte, error) {
	return command(ctx, nil, cfg.cliEnv(), "bin/locoadm", append(args, "--file", cfg.file())...)
}
func writeCA(encoded []byte) error {
	data, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return fmt.Errorf("decode apps CA: %w", err)
	}
	if len(data) == 0 {
		return errors.New("apps TLS Secret contains no ca.crt")
	}
	_, err = io.Copy(os.Stdout, bytes.NewReader(data))
	return err
}
