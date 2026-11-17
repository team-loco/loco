package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"time"
)

func (cfg config) writeState(name string, data []byte) error {
	path := filepath.Join(cfg.State, name)
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (cfg config) cluster(ctx context.Context) error {
	clusters, err := command(ctx, nil, nil, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(string(clusters)), cfg.Cluster) {
		path := filepath.Join(cfg.State, "kind.yaml")
		if err = cfg.render("dev/kind-local.yaml", path); err != nil {
			return err
		}
		_, err = command(
			ctx,
			nil,
			nil,
			"kind",
			"create",
			"cluster",
			"--name",
			cfg.Cluster,
			"--config",
			path,
			"--kubeconfig",
			cfg.kubeconfig(),
		)
		return err
	}
	ports, err := command(
		ctx,
		nil,
		nil,
		"docker",
		"inspect",
		cfg.Cluster+"-control-plane",
		"--format",
		"{{json .HostConfig.PortBindings}}",
	)
	if err != nil {
		return err
	}
	var bindings map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}
	if err = json.Unmarshal(ports, &bindings); err != nil {
		return err
	}
	for containerPort, hostPort := range map[string]string{"30080/tcp": cfg.AppPort, "30443/tcp": cfg.AppsPort} {
		port := bindings[containerPort]
		if len(port) != 1 || port[0].HostIP != "127.0.0.1" || port[0].HostPort != hostPort {
			return errors.New("kind port mappings differ from installation inputs; run mise run down")
		}
	}
	stopped, err := command(
		ctx,
		nil,
		nil,
		"docker",
		"ps",
		"--quiet",
		"--filter",
		"label=io.x-k8s.kind.cluster="+cfg.Cluster,
		"--filter",
		"status=exited",
	)
	if err != nil {
		return err
	}
	if ids := strings.Fields(string(stopped)); len(ids) > 0 {
		if _, err = command(ctx, nil, nil, "docker", append([]string{"start"}, ids...)...); err != nil {
			return err
		}
	}
	if _, err = command(
		ctx,
		nil,
		nil,
		"kind",
		"export",
		"kubeconfig",
		"--name",
		cfg.Cluster,
		"--kubeconfig",
		cfg.kubeconfig(),
	); err != nil {
		return err
	}
	_, err = cfg.kubectl(ctx, "wait", "--for=condition=Ready", "nodes", "--all", "--timeout=180s")
	return err
}

func (cfg config) render(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	parsed, err := template.New(source).Funcs(template.FuncMap{"indent": func(value string, width int) string {
		return strings.Repeat(
			" ",
			width,
		) + strings.ReplaceAll(
			strings.TrimRight(value, "\n"),
			"\n",
			"\n"+strings.Repeat(" ", width),
		)
	}}).Option("missingkey=error").Parse(string(data))
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, cfg); err != nil {
		return err
	}
	return os.WriteFile(destination, output.Bytes(), 0600)
}

const readinessPoll = 2 * time.Second

func (cfg config) waitRunning(ctx context.Context) error {
	// Session refresh in locoadm status needs the API and its gateway route.
	if err := waitAPI(ctx, cfg.apiURL()+"/health"); err != nil {
		return err
	}
	ticker := time.NewTicker(readinessPoll)
	defer ticker.Stop()
	for {
		data, err := cfg.locoadm(ctx, "status")
		if err != nil {
			return err
		}
		ready, err := running(data)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitAPI(ctx context.Context, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(readinessPoll)
	defer ticker.Stop()
	for {
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr == nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				return closeErr
			}
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for API health: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func running(data []byte) (bool, error) {
	var status struct {
		Unclaimed []string `json:"unclaimedAdmins"`
		Clusters  []struct {
			Enrollment string `json:"enrollment"`
			Cluster    struct {
				Name string `json:"name"`
			} `json:"cluster"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return false, err
	}
	if len(status.Unclaimed) > 0 {
		return false, nil
	}
	for _, cluster := range status.Clusters {
		if cluster.Cluster.Name == localIdentity && cluster.Enrollment == "enrolled" {
			return true, nil
		}
	}
	return false, nil
}
