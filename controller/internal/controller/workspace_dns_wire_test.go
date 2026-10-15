package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	testDNSQueryTimeout  = 2 * time.Second
	testDNSReadyTimeout  = 30 * time.Second
	testDNSRetryInterval = 100 * time.Millisecond
	testDNSAlternatePort = "1054"
)

func TestWorkspaceDNSWire(t *testing.T) {
	if os.Getenv("LOCO_TEST_WORKSPACE_DNS") == "" {
		t.Skip("run mise run test:workspace-dns")
	}
	data, err := os.ReadFile("../../../charts/loco-operator/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		WorkspaceDNS WorkspaceDNSConfig `json:"workspaceDNS"`
	}
	if decodeErr := yaml.Unmarshal(data, &values); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	encoded, err := json.Marshal(values.WorkspaceDNS)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ParseWorkspaceDNSConfig(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	name := fmt.Sprintf("loco-workspace-dns-test-%d", os.Getpid())
	docker := func(args ...string) string {
		t.Helper()
		result, err := exec.CommandContext(t.Context(), "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v\n%s", args, err, result)
		}
		return strings.TrimSpace(string(result))
	}
	for _, domain := range []string{config.ClusterDomain, "corp.internal"} {
		t.Run(domain, func(t *testing.T) {
			config.ClusterDomain = domain
			primary := strings.Replace(config.corefile("ws-a"), "/etc/resolv.conf", "127.0.0.1:2053", 1)
			alternate := strings.Replace(config.corefile("ws-b"), ".:1053", ".:"+testDNSAlternatePort, 1)
			alternate = strings.Replace(alternate, "health :8080", "health :8081", 1)
			alternate = strings.Replace(alternate, "ready :8181", "ready :8182", 1)
			alternate = strings.Replace(alternate, "/etc/resolv.conf", "127.0.0.1:2053", 1)
			backend := ".:2053 {\n file /etc/coredns/test.zone .\n}\n"
			zone := fmt.Sprintf(
				`. 2 IN SOA ns. admin. 1 2 2 2 2
. 2 IN NS ns.
ns. 2 IN A 127.0.0.1
cart.ws-a.svc.%s. 2 IN A 192.0.2.10
cart.ws-b.svc.%s. 2 IN A 192.0.2.20
missing.local.svc.%s. 2 IN A 192.0.2.99
external.example. 2 IN A 192.0.2.30
cart.local.example. 2 IN A 192.0.2.40
`,
				domain,
				domain,
				domain,
			)
			if err := os.WriteFile(
				filepath.Join(directory, "Corefile"),
				[]byte(primary+alternate+backend),
				0o644,
			); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "test.zone"), []byte(zone), 0o644); err != nil {
				t.Fatal(err)
			}
			docker(
				"run",
				"-d",
				"--name",
				name,
				"--memory",
				"64m",
				"--cpus",
				"0.25",
				"-p",
				"127.0.0.1::1053/udp",
				"-p",
				"127.0.0.1::1054/udp",
				"-p", "127.0.0.1::1053/tcp",
				"-v",
				directory+":/etc/coredns:ro",
				config.Image,
				"-conf",
				"/etc/coredns/Corefile",
			)
			t.Cleanup(func() { docker("rm", "-f", name) })
			resolver := func(port, protocol string) *net.Resolver {
				address := docker("port", name, port+"/"+protocol)
				return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					dialer := net.Dialer{}
					return dialer.DialContext(ctx, protocol, address)
				}}
			}
			first, second := resolver("1053", "udp"), resolver(testDNSAlternatePort, "udp")
			deadline := time.Now().Add(testDNSReadyTimeout)
			for {
				ctx, cancel := context.WithTimeout(t.Context(), testDNSQueryTimeout)
				_, err := first.LookupIP(ctx, "ip4", "cart.local.")
				cancel()
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("DNS not ready: %v\n%s", err, docker("logs", name))
				}
				time.Sleep(testDNSRetryInterval)
			}
			check := func(r *net.Resolver, query, expected string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), testDNSQueryTimeout)
				defer cancel()
				ips, err := r.LookupIP(ctx, "ip4", query)
				if err != nil {
					t.Fatalf("lookup %s: %v", query, err)
				}
				if len(ips) != 1 || ips[0].String() != expected {
					t.Errorf("%s = %v, want %s", query, ips, expected)
				}
			}
			for _, suffix := range []string{".", ".ws-a.svc." + domain + ".", ".svc." + domain + ".", "." + domain + "."} {
				check(first, "cart.local"+suffix, "192.0.2.10")
				ctx, cancel := context.WithTimeout(t.Context(), testDNSQueryTimeout)
				_, err := first.LookupIP(ctx, "ip4", "missing.local"+suffix)
				cancel()
				var dnsErr *net.DNSError
				if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
					t.Errorf("missing alias %s: want NXDOMAIN, got %v", suffix, err)
				}
			}
			check(second, "cart.local.", "192.0.2.20")
			check(resolver("1053", "tcp"), "cart.local.", "192.0.2.10")
			check(first, "external.example.", "192.0.2.30")
			check(first, "cart.local.example.", "192.0.2.40")
		})
	}
}
