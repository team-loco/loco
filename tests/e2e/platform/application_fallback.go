package platform

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/team-loco/loco/internal/testres"
)

const (
	fallbackAPIHost = "fallback-api.loco-system.svc.cluster.local"
	fallbackAPIPort = 8443
)

// The test API runs on the host. Terminate TLS in a test fixture on kind's network,
// expose it through cluster DNS, and trust its certificate only for the fallback backend.
func (p *Platform) startApplicationFallbackProxy(ctx context.Context) error {
	name := p.composeProject + "-fallback"
	if err := p.lease.Add(ctx, testres.Container, name); err != nil {
		return err
	}
	cert := filepath.Join(p.dir, "fallback.crt")
	key := filepath.Join(p.dir, "fallback.key")
	conf := filepath.Join(p.dir, "fallback.conf")
	if _, err := p.Command(ctx, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
		"-subj", "/CN="+fallbackAPIHost, "-addext", "subjectAltName=DNS:"+fallbackAPIHost,
		"-keyout", key, "-out", cert); err != nil {
		return err
	}
	if err := writeReadable(conf, []byte(fmt.Sprintf(`server {
    listen %d ssl;
    ssl_certificate /etc/fallback/cert;
    ssl_certificate_key /etc/fallback/key;
    location / {
        proxy_pass http://host.docker.internal:%d;
    }
}
`, fallbackAPIPort, p.ports.api))); err != nil {
		return err
	}
	image := fromImage(p.root, "tests/e2e/fixtures/public-image/Dockerfile")
	if err := p.pullImages(ctx, image); err != nil {
		return err
	}
	if err := p.removeContainers(ctx, name); err != nil {
		return err
	}
	if _, err := p.Command(ctx, "docker", "run", "-d", "--name", name, "--network", "kind",
		"--user", "0", "--add-host", "host.docker.internal:host-gateway",
		"-v", conf+":/etc/nginx/conf.d/default.conf:ro",
		"-v", cert+":/etc/fallback/cert:ro", "-v", key+":/etc/fallback/key:ro", image); err != nil {
		return err
	}
	ip, err := p.Command(ctx, "docker", "inspect", "--format", "{{.NetworkSettings.Networks.kind.IPAddress}}", name)
	if err != nil {
		return err
	}
	return p.Apply(ctx, fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: fallback-api
  namespace: %s
spec:
  ports:
    - name: https
      port: %d
---
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: fallback-api
  namespace: %s
  labels:
    kubernetes.io/service-name: fallback-api
addressType: IPv4
ports:
  - name: https
    port: %d
endpoints:
  - addresses: [%q]
`, locoNamespace, fallbackAPIPort, locoNamespace, fallbackAPIPort, strings.TrimSpace(ip)))
}

func (p *Platform) trustApplicationFallbackProxy(ctx context.Context) error {
	namespace := workerRelease + "-fallback"
	cert := filepath.Join(p.dir, "fallback.crt")
	manifest, err := p.Kubectl(ctx, "-n", namespace, "create", "configmap", "fallback-ca",
		"--from-file=ca.crt="+cert, "--dry-run=client", "-o", "yaml")
	if err != nil {
		return err
	}
	if err = p.Apply(ctx, manifest); err != nil {
		return err
	}
	_, err = p.Kubectl(
		ctx, "-n", namespace, "patch", "backendtlspolicy", "control-plane", "--type=merge", "-p",
		`{"spec":{"validation":{"wellKnownCACertificates":null,"caCertificateRefs":[`+
			`{"group":"","kind":"ConfigMap","name":"fallback-ca"}]}}}`,
	)
	return err
}
