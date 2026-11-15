package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (cfg config) registryConfig() error {
	data, err := os.ReadFile("dev/registry/config.json")
	if err != nil {
		return err
	}
	var registry map[string]any
	if err = json.Unmarshal(data, &registry); err != nil {
		return err
	}
	httpConfig, ok := registry["http"].(map[string]any)
	if !ok {
		return errors.New("registry config has no http object")
	}
	httpConfig["port"] = cfg.RegistryPort
	data, err = json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.State, "registry.json"), data, 0600)
}

func (cfg config) registryHosts(ctx context.Context) error {
	host := "registry." + cfg.Domain + ":" + cfg.RegistryPort
	directory := "/etc/containerd/certs.d/" + host
	// The node's Docker-network alias serves the same port the host registry exposes.
	content := fmt.Sprintf(
		"server = %q\n[host.%q]\n  capabilities = [\"pull\", \"resolve\"]\n",
		"http://"+host,
		"http://"+host,
	)
	nodes, err := command(ctx, nil, nil, "kind", "get", "nodes", "--name", cfg.Cluster)
	if err != nil {
		return err
	}
	for node := range strings.FieldsSeq(string(nodes)) {
		if _, err := command(ctx, nil, nil, "docker", "exec", node, "mkdir", "-p", directory); err != nil {
			return err
		}
		if _, err := command(
			ctx,
			[]byte(content),
			nil,
			"docker",
			"exec",
			"-i",
			node,
			"cp",
			"/dev/stdin",
			directory+"/hosts.toml",
		); err != nil {
			return err
		}
	}
	return nil
}

func (cfg config) gatewayDNS(ctx context.Context) error {
	data, err := cfg.kubectl(ctx, "-n", "kube-system", "get", "configmap", "coredns", "-o", "json")
	if err != nil {
		return err
	}
	var configMap struct {
		Data map[string]string `json:"data"`
	}
	if err = json.Unmarshal(data, &configMap); err != nil {
		return err
	}
	corefile, err := gatewayCorefile(configMap.Data["Corefile"], cfg.Domain)
	if err != nil {
		return err
	}
	patch, err := json.Marshal(map[string]any{"data": map[string]string{"Corefile": corefile}})
	if err != nil {
		return err
	}
	_, err = cfg.kubectl(ctx, "-n", "kube-system", "patch", "configmap", "coredns", "--type=merge", "-p", string(patch))
	if err != nil {
		return err
	}
	_, err = cfg.kubectl(ctx, "-n", "kube-system", "rollout", "restart", "deployment/coredns")
	return err
}

func gatewayCorefile(corefile, domain string) (string, error) {
	const marker = "    kubernetes cluster.local"
	if !strings.Contains(corefile, marker) {
		return "", errors.New("CoreDNS Corefile has no Kubernetes plugin")
	}
	escaped := strings.ReplaceAll(domain, ".", `\.`)
	rules := fmt.Sprintf(
		`    rewrite stop name regex ^(api|ingest)\.%s$ control-plane-gateway.loco-control-plane.svc.cluster.local answer auto
    rewrite stop name regex ^.+\.apps\.%s$ apps-gateway.loco-system.svc.cluster.local answer auto
`,
		escaped,
		escaped,
	)
	if strings.Contains(corefile, rules) {
		return corefile, nil
	}
	return strings.Replace(corefile, marker, rules+marker, 1), nil
}

func (cfg config) gatewayServices(ctx context.Context) error {
	return cfg.apply(ctx, []byte(fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: loco-control-plane
---
apiVersion: v1
kind: Namespace
metadata:
  name: loco-system
  labels:
    pod-security.kubernetes.io/enforce: privileged
---
apiVersion: v1
kind: Service
metadata:
  name: control-plane-gateway
  namespace: loco-control-plane
spec:
  type: NodePort
  selector:
    gateway.envoyproxy.io/owning-gateway-name: loco-control-plane
    gateway.envoyproxy.io/owning-gateway-namespace: loco-control-plane
  ports:
    - name: http
      port: %s
      targetPort: 10080
      nodePort: 30080
---
apiVersion: v1
kind: Service
metadata:
  name: apps-gateway
  namespace: loco-system
spec:
  type: NodePort
  selector:
    gateway.envoyproxy.io/owning-gateway-name: eg
    gateway.envoyproxy.io/owning-gateway-namespace: loco-system
  ports:
    - name: https
      port: %s
      targetPort: 10443
      nodePort: 30443
`, cfg.AppPort, cfg.AppsPort)))
}

func (cfg config) inputSecrets(ctx context.Context) error {
	database := "postgres://loco_user:loco_password@postgres." + cfg.Domain + ":5432/loco?sslmode=disable"
	secrets := []struct {
		Name string
		Data map[string]string
	}{
		{"loco-inputs", map[string]string{"database-url": database}},
		//nolint:gosec // Public development fixture credentials match dev/compose.yaml.
		{"loco-source", map[string]string{"access-key-id": "loco-dev", "secret-access-key": "loco-dev-secret"}},
		//nolint:gosec // Public development accounts match dev/registry/htpasswd.
		{"loco-registry", map[string]string{
			"api-username": "api", "api-password": "loco-dev-api",
			"push-username": "builder", "push-password": "loco-dev-builder",
			"pull-username": "nodes", "pull-password": "loco-dev-nodes",
		}},
	}
	for _, secret := range secrets {
		data, err := json.Marshal(
			map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata":   map[string]string{"name": secret.Name, "namespace": "loco-control-plane"},
				"stringData": secret.Data,
			},
		)
		if err != nil {
			return err
		}
		if err := cfg.apply(ctx, data); err != nil {
			return err
		}
	}
	return nil
}
