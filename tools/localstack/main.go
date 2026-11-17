package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/team-loco/loco/internal/envconfig"
)

type config struct {
	Cluster, Domain, State, Project                           string
	AppPort, AppsPort, WebPort, DexPort, RegistryPort, S3Port string
	PrivateEgress                                             string
	BundleVersion, PushAuth, PullAuth                         string
	Timeout                                                   time.Duration
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg := newConfig()
	ctx, deadline := context.WithTimeout(ctx, cfg.Timeout)
	defer deadline()
	if err := run(ctx, cfg, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newConfig() config {
	required := func(name string) string {
		value := envconfig.String(name, "")
		if value == "" {
			panic(name + " is required; run this tool through mise")
		}
		return value
	}
	app := required("APP_PORT")
	return config{
		Project: required(
			"COMPOSE_PROJECT_NAME",
		),
		Cluster:  required("KIND_CLUSTER"),
		Domain:   required("STACK_DOMAIN"),
		State:    required("STACK_STATE"),
		AppPort:  app[strings.LastIndex(app, ":")+1:],
		AppsPort: required("APPS_PORT"),
		WebPort: required(
			"WEB_PORT",
		),
		DexPort:      required("DEX_PORT"),
		RegistryPort: required("REGISTRY_PORT"),
		S3Port:       required("S3_PORT"),
		Timeout:      envconfig.PositiveDuration("LOCAL_STACK_TIMEOUT", 45*time.Minute),
	}
}

func run(ctx context.Context, cfg config, args []string) error {
	if len(args) == 0 || len(args) > 2 ||
		(len(args) == 2 && (args[0] != "deploy" || args[1] != "--suspend")) {
		return errors.New("usage: localstack install|deploy [--suspend]|ca")
	}
	switch args[0] {
	case "install":
		return cfg.install(ctx)
	case "deploy":
		return cfg.deploy(ctx, len(args) == 2 && args[1] == "--suspend")
	case "ca":
		data, err := cfg.kubectl(
			ctx,
			"-n",
			"loco-system",
			"get",
			"secret",
			"loco-worker-tls",
			"-o",
			"jsonpath={.data.ca\\.crt}",
		)
		if err != nil {
			return err
		}
		return writeCA(data)
	default:
		return fmt.Errorf("unknown localstack command %q", args[0])
	}
}

func (cfg config) home() string   { return filepath.Join(cfg.State, "home") }
func (cfg config) apiURL() string { return "http://api." + cfg.Domain + ":" + cfg.AppPort }
