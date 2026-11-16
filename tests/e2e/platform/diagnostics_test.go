package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeCluster(t *testing.T, failCreate bool) (*Cluster, string) {
	t.Helper()
	c := NewCluster("diagnostic-test")
	c.dir = t.TempDir()
	c.kubeconfig = filepath.Join(c.dir, "kubeconfig")
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := `#!/bin/sh
printf '%s %s\n' "${0##*/}" "$*" >> "$CALLS"
case "${0##*/} $*" in
 'kind get clusters') exit 0;;
 'kind create cluster'*)
  if [ "$FAIL_CREATE" = true ]; then echo 'deliberate kind failure' >&2; exit 7; fi;;
 *'crictl ps'*) echo container-id;;
 *'crictl logs'*)
  if [ -n "$FAIL_LOGS" ] && [ -f "$FAIL_LOGS" ]; then echo 'unable to retrieve container logs' >&2; exit 1; fi
  if [ -n "$LOG_FILE" ]; then cat "$LOG_FILE"; else echo 'control-plane failure evidence'; fi;;
 'kubectl '*'-o json') if [ -n "$PODS_FILE" ]; then cat "$PODS_FILE"; else echo '{"items":[]}'; fi;;
 *) echo 'diagnostic evidence';;
esac
`
	for _, name := range []string{"kind", "docker", "kubectl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c.env = append(os.Environ(), "CALLS="+calls)
	if failCreate {
		c.env = append(c.env, "FAIL_CREATE=true")
	}
	return c, calls
}

func TestSetupFailureDiagnostics(t *testing.T) {
	for _, kindFailure := range []bool{true, false} {
		name := "crds"
		if kindFailure {
			name = "kind"
		}
		t.Run(name, func(t *testing.T) {
			c, calls := fakeCluster(t, kindFailure)
			ctx, cancel := context.WithCancel(t.Context())
			setupCalled := false
			code := c.runTests(ctx, nil, func(context.Context) error {
				setupCalled = true
				cancel()
				return errors.New("deliberate CRD failure")
			})
			if code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			if setupCalled == kindFailure {
				t.Fatalf("setup called = %v", setupCalled)
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			commands := string(data)
			for _, want := range []string{
				"--retain", "journalctl", "--name etcd", "--name kube-apiserver", "crictl logs", "get events",
				"--name kube-scheduler", "--name kube-controller-manager", "get --raw=/readyz?verbose", "get leases", "docker stats",
			} {
				if !strings.Contains(commands, want) {
					t.Errorf("commands lack %q:\n%s", want, commands)
				}
			}
			if got := strings.Count(commands, "kind create cluster"); got != 1 {
				t.Fatalf("creation attempts = %d, want 1", got)
			}
		})
	}
}

func TestNamespaceDiagnosticsSurviveCancellation(t *testing.T) {
	c, calls := fakeCluster(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.LogNamespace(canceledDiagnosticTest{TB: t, ctx: ctx}, "fixtures")
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-n fixtures get pods", "containerStatuses", "-n fixtures get events"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("commands lack %q: %s", want, data)
		}
	}
}

type canceledDiagnosticTest struct {
	testing.TB
	ctx context.Context
}

func (t canceledDiagnosticTest) Context() context.Context { return t.ctx }

func TestDiagnosticCapturesSuccessfulStderr(t *testing.T) {
	c, _ := fakeCluster(t, false)
	out, err := c.diagnostic(t.Context(), "sh", "-c", "printf diagnostic-stderr >&2")
	if err != nil {
		t.Fatal(err)
	}
	if out != "diagnostic-stderr" {
		t.Fatalf("output = %q, want stderr", out)
	}
}
