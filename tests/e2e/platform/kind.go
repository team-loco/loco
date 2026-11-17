package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
)

// setupKind reuses the run's kind cluster when it is reachable and runs the same node image, and
// creates it otherwise.
func (c *Cluster) setupKind(ctx context.Context) error {
	node := kindNodeImage(c.root)
	clusters, err := c.Command(ctx, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if slices.Contains(strings.Fields(clusters), c.cluster) {
		retained, err := c.Command(ctx, "docker", "inspect", "--format", "{{.Config.Image}}", c.Node())
		if err != nil {
			return err
		}
		if strings.TrimSpace(retained) != node {
			return fmt.Errorf(
				"retained cluster %s runs %s, not %s; run mise run e2e:teardown %s before changing Kubernetes versions",
				c.cluster,
				strings.TrimSpace(retained),
				node,
				c.shard,
			)
		}
		if err := c.writeKubeconfig(ctx); err != nil {
			return err
		}
		if _, err := c.Kubectl(ctx, "cluster-info"); err == nil {
			c.clusterReused = true
			logf("Reusing kind cluster %s", c.cluster)
			return nil
		}
		logf("Kind cluster %s is unreachable, recreating it", c.cluster)
		if _, err := c.Command(
			ctx,
			"kind",
			"delete",
			"cluster",
			"--name",
			c.cluster,
			"--kubeconfig",
			c.kubeconfig,
		); err != nil {
			return err
		}
	}
	if _, err := c.Command(
		ctx,
		"kind",
		"create",
		"cluster",
		"--retain",
		"--image",
		node,
		"--config",
		filepath.Join(
			c.root,
			"tests",
			"e2e",
			"kind-e2e.yml",
		),
		"--name",
		c.cluster,
		"--kubeconfig",
		c.kubeconfig,
	); err != nil {
		return err
	}
	return c.writeKubeconfig(ctx)
}

func (c *Cluster) writeKubeconfig(ctx context.Context) error {
	config, err := c.Command(ctx, "kind", "get", "kubeconfig", "--name", c.cluster)
	if err != nil {
		return err
	}
	return os.WriteFile(c.kubeconfig, []byte(config), 0o600)
}

// installWorker installs the loco-worker chart; with collectors it adds the OpenTelemetry
// collectors exporting to the ingest container.
func (p *Platform) installWorker(ctx context.Context, images *images, collectors bool) error {
	if err := p.gatewayTLSSecret(ctx); err != nil {
		return err
	}
	args := []string{
		"upgrade", "--install", workerRelease, filepath.Join(p.root, "charts", "loco-worker"),
		"--kubeconfig", p.kubeconfig, "--kube-context", p.kubeContext, "--namespace", locoNamespace,
		"--values", filepath.Join(p.root, "charts", "loco-worker", "tests", "kind-values.yaml"),
		"--values", filepath.Join(p.root, "tests", "e2e", "worker-values.yaml"),
		setFlag, "global.domain.apps={" + platformDomain + "}",
		setFlag, "certManager.existingTLSSecret=" + gatewayTLSSecret,
		setFlag, "controller.image.repository=" + repository(images.controller),
		setFlag, "controller.image.tag=" + tag(images.controller),
	}
	if p.Builds {
		egress, err := json.Marshal(p.buildEgress)
		if err != nil {
			return err
		}
		args = append(args,
			setFlag, "builds.builderImage.repository="+repository(images.builder),
			setFlag, "builds.builderImage.tag="+tag(images.builder),
			"--set-json", "builds.privateEgress="+string(egress))
	} else {
		args = append(args, setFlag, "cluster.role=apps")
	}
	if collectors {
		args = append(args, "--values", filepath.Join(p.root, "tests", "e2e", "obs-values.yaml"),
			setFlag, fmt.Sprintf("global.exporter.endpoint=http://%s:%d", p.ingestContainer(), p.values.ingestHTTPPort))
	} else {
		args = append(
			args,
			"--set",
			"otel-col-daemon.enabled=false",
			"--set",
			"otel-col-deploy.enabled=false",
			"--set",
			"otel-col-cluster.enabled=false",
		)
	}
	if p.Observability {
		args = append(args, setFlag, "global.exporter.ingestToken.enabled=true")
	}
	if p.RegistryToken {
		args = append(
			args,
			"--set",
			"registry.auth=token",
			"--set",
			"registry.pullSecretName=",
			"--set",
			"builds.pushSecretName=",
			"--set",
			"agent.registryCredentialCheckInterval="+registryCredentialCheckInterval,
		)
	}
	args = append(args, "--wait", "--timeout", rolloutTimeout.String())
	if _, err := p.Command(ctx, "helm", args...); err != nil {
		return err
	}
	if !p.Builds && p.clusterReused {
		if _, err := p.Kubectl(
			ctx,
			"delete",
			"namespace",
			p.BuildNamespace,
			"--ignore-not-found",
			"--wait",
			"--timeout",
			readyTimeout.String(),
		); err != nil {
			return err
		}
	}
	if err := p.readAgentEnv(ctx); err != nil {
		return err
	}
	if err := waitFor(ctx, rolloutTimeout, "the Envoy proxy Deployment", func(ctx context.Context) error {
		found, err := p.Exists(ctx, "-n", locoNamespace, "deployments", "-l", gatewaySelector)
		if err == nil && !found {
			return errNotYet
		}
		return err
	}); err != nil {
		return err
	}
	_, err := p.Kubectl(
		ctx,
		"-n",
		locoNamespace,
		"rollout",
		"status",
		"deployment",
		"-l",
		gatewaySelector,
		"--timeout",
		rolloutTimeout.String(),
	)
	return err
}

// readAgentEnv reads the agent's environment from the chart's agent Deployment, which the chart
// installs without replicas, so the host agent runs with the settings the chart would give it.
func (p *Platform) readAgentEnv(ctx context.Context) error {
	manifest, err := p.Command(ctx, "helm", "get", "manifest", workerRelease, "--kubeconfig", p.kubeconfig,
		"--kube-context", p.kubeContext, "--namespace", locoNamespace)
	if err != nil {
		return err
	}
	out, err := p.commandStdin(ctx, []byte(manifest), "yq", "-o", "json",
		`select(.kind == "Deployment" and .metadata.name == "`+workerRelease+`-agent")`)
	if err != nil {
		return err
	}
	var deployment appsv1.Deployment
	if err := json.Unmarshal([]byte(out), &deployment); err != nil {
		return fmt.Errorf("decode the agent Deployment: %w", err)
	}
	p.agentEnv = nil
	for _, variable := range deployment.Spec.Template.Spec.Containers[0].Env {
		if variable.ValueFrom == nil {
			p.agentEnv = append(p.agentEnv, variable)
		}
	}
	return nil
}

func (p *Platform) agentVariable(name string) string {
	for _, variable := range p.agentEnv {
		if variable.Name == name {
			return variable.Value
		}
	}
	return ""
}

func (p *Platform) gatewayTLSSecret(ctx context.Context) error {
	key := filepath.Join(p.dir, "gateway-tls.key")
	cert := filepath.Join(p.dir, "gateway-tls.crt")
	if _, err := p.Command(ctx, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
		"-subj", "/CN=*."+platformDomain, "-addext", "subjectAltName=DNS:*."+platformDomain,
		"-keyout", key, "-out", cert); err != nil {
		return err
	}
	secret, err := p.Kubectl(ctx, "-n", locoNamespace, "create", "secret", "tls", gatewayTLSSecret,
		"--cert", cert, "--key", key, "--dry-run=client", "-o", "yaml")
	if err != nil {
		return err
	}
	return p.Apply(ctx, secret)
}

const setFlag = "--set"

func repository(reference string) string { return reference[:strings.LastIndex(reference, ":")] }
func tag(reference string) string        { return reference[strings.LastIndex(reference, ":")+1:] }
