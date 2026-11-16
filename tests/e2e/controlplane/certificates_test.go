package controlplane

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

const (
	certificatesNamespace = "loco-certificates"
	certificateTemplate   = "templates/cm-cert.yaml"
)

// TestCertificates issues worker certificates from chart-owned and referenced issuers.
func TestCertificates(t *testing.T) {
	t.Parallel()
	c.MustApply(t, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: "+certificatesNamespace+"\n")
	t.Cleanup(func() {
		if t.Failed() {
			c.LogKubectl(t, "get pods,certificates,issuers,clusterissuers -A -o wide",
				"get events -A --sort-by=.lastTimestamp", "-n cert-manager logs deployment/cert-manager")
		}
	})
	render := func(release, template string, sets ...string) string {
		sets = append([]string{"gateway-helm.enabled=false", "agent.image.tag=e2e",
			"controller.image.tag=e2e", "cluster.role=apps", "otel-col-daemon.enabled=false",
			"otel-col-deploy.enabled=false", "otel-col-cluster.enabled=false", "certManager.issuer.type=selfSigned",
			"global.domain.platform=platform.example.test"}, sets...)
		args := make([]string, 0, 9+len(sets))
		args = append(args, "template", release, c.Path("charts", "loco-worker"), "-n", certificatesNamespace,
			"--show-only", template, "--set-json", `global.domain.apps=["apps.example.test"]`)
		for _, set := range sets {
			args = append(args, "--set="+set)
		}
		return helm(t, args...)
	}

	t.Run("the chart's issuer", func(t *testing.T) {
		const issuer = "certManager.issuer.name=loco-certificates-local"
		c.MustApply(t, render("local", "templates/cm-issuer.yaml", issuer))
		c.MustApply(t, render("local", certificateTemplate, issuer))
		checkCertificate(t, "local", "probe.apps.example.test")
	})

	t.Run("an HTTP control plane's issuer", func(t *testing.T) {
		const issuer = "loco-certificates-http"
		manifest := helm(t, "template", "http-control-plane", c.Path("charts", "loco-control-plane"),
			"-f", c.Path("charts", "loco-control-plane", "ci", "default-values.yaml"),
			"-f", c.Path("charts", "loco-control-plane", "tests", "values", "hosts.yaml"),
			"--set=certManager.enabled=false", "--set=certManager.issuer.type=selfSigned",
			"--set=certManager.issuer.name="+issuer, "--show-only", "templates/cm-issuer.yaml")
		c.MustApply(t, manifest)
		c.MustKubectl(t, "wait", "clusterissuer/"+issuer, "--for=condition=Ready",
			"--timeout="+installTimeout.String())
		c.MustApply(t, render("http-worker", certificateTemplate, "certManager.issuer.create=false",
			"certManager.issuer.name="+issuer, "certManager.issuer.kind=ClusterIssuer"))
		checkCertificate(t, "http-worker", "probe.apps.example.test")
	})

	t.Run("an existing issuer", func(t *testing.T) {
		c.MustApply(t, `apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: existing-local
  namespace: `+certificatesNamespace+`
spec:
  selfSigned: {}
`)
		c.MustApply(t, render("existing", certificateTemplate, "certManager.issuer.create=false",
			"certManager.issuer.name=existing-local", "certManager.issuer.kind=Issuer"))
		checkCertificate(t, "existing", "probe.platform.example.test")
	})
}

// checkCertificate waits for the release's Certificate and checks that its certificate covers host.
func checkCertificate(t *testing.T, release, host string) {
	t.Helper()
	c.MustKubectl(t, "-n", certificatesNamespace, "wait", "certificate/"+release+"-cert", "--for=condition=Ready",
		"--timeout="+installTimeout.String())
	encoded := c.MustKubectl(t, "-n", certificatesNamespace, "get", "secret", release+"-tls",
		"-o", `jsonpath={.data.tls\.crt}`)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("secret %s-tls holds no PEM certificate", release)
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.VerifyHostname(host); err != nil {
		t.Errorf("certificate of %s: %v", release, err)
	}
}
