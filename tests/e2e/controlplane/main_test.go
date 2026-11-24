// Package controlplane runs the chart suites of a bare kind cluster: the loco-control-plane chart
// with in-cluster Postgres, ClickHouse and OpenBao, and the loco-worker chart's certificates and
// platform telemetry collectors, each installed on its own beside the others.
package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/team-loco/loco/tests/e2e/platform"
)

const installTimeout = 5 * time.Minute

var c = platform.NewCluster("controlplane")

func TestMain(m *testing.M) {
	os.Exit(c.Run(m, setup))
}

// setup installs what the suites share: the CRDs, cert-manager and the CloudNativePG operator, each
// from the chart the platform bundle mirrors.
func setup(ctx context.Context) error {
	if _, err := c.Mise(ctx, []string{"KIND_CLUSTER=" + c.KindCluster()}, "crds:apply"); err != nil {
		return err
	}
	for _, upstream := range []struct{ release, namespace, set string }{
		{"cert-manager", "cert-manager", "crds.enabled=true"},
		{"cloudnative-pg", "cnpg-system", "crds.create=true"},
	} {
		var chart struct {
			Repository string `json:"repository"`
			Chart      string `json:"chart"`
			Version    string `json:"version"`
		}
		if err := platform.ReadYAML(c.Path("charts", upstream.release+".yaml"), &chart); err != nil {
			return err
		}
		if _, err := c.Helm(ctx, "upgrade", "--install", upstream.release, chart.Chart,
			"--repo", chart.Repository, "--version", chart.Version,
			"--namespace", upstream.namespace, "--create-namespace", "--set", upstream.set,
			"--wait=legacy", "--timeout", installTimeout.String()); err != nil {
			return err
		}
	}
	return nil
}

func mustReadYAML(tb testing.TB, path string, into any) {
	tb.Helper()
	if err := platform.ReadYAML(path, into); err != nil {
		tb.Fatal(err)
	}
}

// helm runs a helm command against the cluster and fails the test when it fails.
func helm(tb testing.TB, args ...string) string {
	tb.Helper()
	out, err := c.Helm(tb.Context(), args...)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// renderedDocuments splits helm template's output into its objects.
func renderedDocuments(tb testing.TB, rendered string) []map[string]any {
	tb.Helper()
	var documents []map[string]any
	for document := range strings.SplitSeq(rendered, "\n---") {
		var object map[string]any
		if err := yaml.Unmarshal([]byte(document), &object); err != nil {
			tb.Fatal(err)
		}
		if object != nil {
			documents = append(documents, object)
		}
	}
	return documents
}

// applySecret creates or updates an Opaque Secret.
func applySecret(tb testing.TB, namespace, name string, data map[string]string) {
	tb.Helper()
	c.MustApply(tb, secretManifest(tb, namespace, name, data))
}

func secretManifest(tb testing.TB, namespace, name string, data map[string]string) string {
	tb.Helper()
	manifest, err := json.Marshal(corev1.Secret{
		APIVersion: "v1", Kind: "Secret",
		Name: name, Namespace: namespace,
		StringData: data,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return string(manifest)
}

func applyJSON(tb testing.TB, objects ...any) {
	tb.Helper()
	for _, object := range objects {
		manifest, err := json.Marshal(object)
		if err != nil {
			tb.Fatal(err)
		}
		c.MustApply(tb, string(manifest))
	}
}

// writeJSON writes a values file for helm under the run's directory.
func writeJSON(tb testing.TB, name string, value any) string {
	tb.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		tb.Fatal(err)
	}
	return writeFile(tb, name, string(data))
}

// writeFile writes a file under the run's directory and returns its path.
func writeFile(tb testing.TB, name, content string) string {
	tb.Helper()
	path := filepath.Join(c.Dir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatal(err)
	}
	return path
}

func randomBase64(size int) string {
	data := make([]byte, size)
	_, _ = rand.Read(data)
	return base64.StdEncoding.EncodeToString(data)
}
