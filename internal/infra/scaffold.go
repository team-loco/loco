package infra

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	loco "github.com/team-loco/loco/sdk/go"
)

const (
	DefaultPlatformDomain = "onloco.app"
	SDKVersion            = "v0.1.0"
)

func Scaffold(projectRoot, name, domain string, force bool) error {
	if err := loco.Normalize(&loco.Manifest{Version: loco.ProtocolVersion, Stack: loco.Stack{Name: name}}); err != nil {
		return err
	}
	dir := filepath.Join(projectRoot, ".loco")
	files := map[string]string{
		"go.mod": "module " + name + "-infra\n\ngo 1.24.0\n\n" +
			"require github.com/team-loco/loco/sdk/go " + SDKVersion + "\n",
		"main.go": fmt.Sprintf(
			scaffoldMain,
			strconv.Quote(name),
			strconv.Quote(name),
			strconv.Quote(name),
			strconv.Quote(domain),
		),
	}
	for name := range files {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil && !force {
			return fmt.Errorf(".loco/%s already exists; use --force to overwrite", name)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check definition: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create definition directory: %w", err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			return fmt.Errorf("write definition: %w", err)
		}
	}
	return nil
}

const scaffoldMain = `package main

import loco "github.com/team-loco/loco/sdk/go"

func main() {
	loco.Run(func(ctx loco.Context) loco.Stack {
		return loco.Stack{
			Name: %s,
			Services: []loco.Service{
				{
					Key: %s,
					Build: &loco.DockerBuild{Context: ".", Dockerfile: "Dockerfile"},
					Routing: loco.Routing{Port: 8000},
					Domain: &loco.PlatformDomain{Hostname: %s + "-" + ctx.Environment + "." + %s},
					PrimaryRegion: "us-east-1",
					Regions: map[string]loco.Region{
						"us-east-1": {CPU: "100m", Memory: "256Mi", ReplicasMin: 1, ReplicasMax: 1},
					},
					Health: loco.Health{Path: "/health"},
				},
			},
		}
	})
}
`
