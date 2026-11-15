package platform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	postgresUser     = "loco_e2e"
	postgresPassword = "loco_e2e_pass" //nolint:gosec // the throwaway database of the run
	postgresDB       = "loco_e2e"
	sourceBucket     = "loco-e2e-sources"
	sourceAccessKey  = "loco-e2e"
	sourceSecretKey  = "loco-e2e-secret" //nolint:gosec // the throwaway bucket of the run
)

// commandEnv is the environment of every command the run starts: the compose variables of
// dev/compose.yaml and tests/e2e/compose.kind.yaml, and the run's kubeconfig.
func (p *Platform) commandEnv() []string {
	return append(os.Environ(),
		"KUBECONFIG="+p.kubeconfig,
		"E2E_ROOT_DIR="+p.root,
		"E2E_AUTH_ISSUER="+p.authIssuer(),
		"POSTGRES_USER="+postgresUser,
		"POSTGRES_PASSWORD="+postgresPassword,
		"POSTGRES_DB="+postgresDB,
		"POSTGRES_PORT=127.0.0.1:",
		"OPENBAO_PORT=127.0.0.1:",
		"OPENBAO_IMAGE="+p.values.openbaoImage,
		"REGISTRY_PORT=127.0.0.1:"+strconv.Itoa(p.ports.registry),
		"S3_PORT="+strconv.Itoa(p.ports.s3),
		"DEX_PORT=127.0.0.1:"+strconv.Itoa(p.ports.auth),
		"E2E_REGISTRY_ALIAS="+p.registryAlias(),
		"E2E_PUBLIC_REGISTRY_ALIAS="+p.composeProject+"-public-image",
		"E2E_S3_ALIAS="+p.s3Alias(),
		"E2E_REGISTRY_TOKEN_DIR="+p.registryTokenDir(),
		"LOCO_SOURCE_BUCKET="+sourceBucket,
		"LOCO_SOURCE_BUCKET_ACCESS_KEY_ID="+sourceAccessKey,
		"LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY="+sourceSecretKey,
		"LOCO_SOURCE_BUCKET_REGION="+clusterRegion,
	)
}

// Command runs a program with the run's environment and returns its standard output. Its error
// carries the standard error.
func (p *Platform) Command(ctx context.Context, name string, args ...string) (string, error) {
	return p.commandStdin(ctx, nil, name, args...)
}

func (p *Platform) commandStdin(
	ctx context.Context,
	stdin []byte,
	name string,
	args ...string,
) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec,nolintlint // the run's own tools
	cmd.Env = p.env
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err,
			strings.TrimSpace(stderr.String()+"\n"+stdout.String()))
	}
	return stdout.String(), nil
}

func (p *Platform) compose(ctx context.Context, args ...string) (string, error) {
	files := []string{
		"compose",
		"-f", filepath.Join(p.root, "dev", "compose.yaml"),
		"-f", filepath.Join(p.root, "tests", "e2e", "compose.kind.yaml"),
	}
	if p.RegistryToken {
		files = append(files, "-f", filepath.Join(p.root, "tests", "e2e", "compose.registry-token.yaml"))
	}
	return p.Command(ctx, "docker", append(append(files, "-p", p.composeProject), args...)...)
}

func (p *Platform) composePort(ctx context.Context, service string, port int) (int, error) {
	published, err := p.compose(ctx, "port", service, strconv.Itoa(port))
	if err != nil {
		return 0, err
	}
	published = strings.TrimSpace(published)
	return strconv.Atoi(published[strings.LastIndex(published, ":")+1:])
}

// mise runs a mise task of the repository.
func (p *Platform) mise(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "mise", append([]string{"run", "--quiet"}, args...)...)
	cmd.Dir = p.root
	cmd.Env = append(append([]string{}, p.env...), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"mise run %s: %w: %s",
			strings.Join(args, " "),
			err,
			strings.TrimSpace(stderr.String()+stdout.String()),
		)
	}
	return stdout.String(), nil
}

func (p *Platform) registryAlias() string    { return p.composeProject + "-registry" }
func (p *Platform) s3Alias() string          { return p.composeProject + "-s3.localhost" }
func (p *Platform) realmContainer() string   { return p.composeProject + "-realm" }
func (p *Platform) clickhouseName() string   { return p.composeProject + "-clickhouse" }
func (p *Platform) ingestContainer() string  { return p.composeProject + "-obs-ingest" }
func (p *Platform) registryTokenDir() string { return filepath.Join(p.dir, "registry") }
func (p *Platform) authIssuer() string {
	return "http://127.0.0.1:" + strconv.Itoa(p.ports.auth) + "/dex"
}
