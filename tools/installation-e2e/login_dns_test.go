package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestDNSQueryResult(t *testing.T) {
	for _, tc := range []struct {
		output  string
		success bool
		want    string
	}{
		{"Server: 10.96.0.10\nAddress: 10.96.0.10#53\n" +
			"Name: api.installation.test\nAddress: 172.18.0.4\ncredential-canary", true, "172.18.0.4"},
		{"Address: fd00::4\n", true, "fd00::4"},
		{"credential-canary NXDOMAIN", false, "NXDOMAIN"},
		{"SERVFAIL", false, "SERVFAIL"},
		{"REFUSED", false, "REFUSED"},
		{"dns-private-value", false, "query-unavailable"},
		{"dns-private-value", true, "no-addresses"},
		{strings.Repeat("Address: 172.18.0.4\n", 10000), true, strings.TrimSuffix(strings.Repeat("172.18.0.4,", 8), ",")},
	} {
		if got := dnsQueryResult([]byte(tc.output), tc.success); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestLoginDNSFailureProjectsFixtureNames(t *testing.T) {
	output := captureStderr(t, func() {
		reportLoginDNSFailure(
			[]byte("credential-canary lookup api.installation.test on 10.96.0.10:53: no such host password-canary"),
		)
		reportLoginDNSFailure([]byte("lookup dex.installation-fixtures.svc.cluster.local: server misbehaving"))
		reportLoginDNSFailure([]byte("lookup token-canary: no such host"))
	})
	want := dnsDiagnosticPrefix + "failed lookup host=api.installation.test result=no such host\n" +
		dnsDiagnosticPrefix + "failed lookup host=dex.installation-fixtures.svc.cluster.local result=server misbehaving\n"
	if output != want {
		t.Fatalf("output = %q", output)
	}
}

func TestFailedLoginCollectsDNSFromOperatorNamespace(t *testing.T) {
	fakeOperatorCommand(t, `#!/bin/sh
case "$*" in
 *'installation-e2e --client'*) printf 'installation-e2e login failure: api-dns\n'; exit 1;;
 '-n kube-system get deployment coredns -o json')
 echo '{"spec":{"replicas":2},"status":{"readyReplicas":1,"availableReplicas":1,"updatedReplicas":2},
 "credential":"credential-canary"}';;
 '-n installation-fixtures exec operator -- nslookup -type=A api.installation.test')
 printf 'Address: 10.96.0.10#53\nAddress: 172.18.0.4\ncredential-canary';;
 '-n installation-fixtures exec operator -- nslookup -type=AAAA api.installation.test') echo NXDOMAIN; exit 1;;
 '-n installation-fixtures exec operator -- nslookup -type=A dex.installation-fixtures.svc.cluster.local')
 echo SERVFAIL; exit 1;;
 '-n installation-fixtures exec operator -- nslookup -type=AAAA dex.installation-fixtures.svc.cluster.local')
 echo credential-canary >&2; exit 1;;
 *) exit 42;;
esac
`)
	output := captureStderr(t, func() {
		if _, err := operatorWorkflow(t.Context(), t.TempDir()); err == nil {
			t.Fatal("expected login failure")
		}
	})
	for _, want := range []string{
		"CoreDNS desired=2 ready=1 available=1 updated=2",
		"host=api.installation.test type=A result=172.18.0.4",
		"host=api.installation.test type=AAAA result=NXDOMAIN",
		"host=dex.installation-fixtures.svc.cluster.local type=A result=SERVFAIL",
		"host=dex.installation-fixtures.svc.cluster.local type=AAAA result=query-unavailable",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in %q", want, output)
		}
	}
	if strings.Contains(output, "canary") {
		t.Fatal("private command output escaped")
	}
}

func TestDNSDiagnosticsSurviveCancellationAndRejectOversizedOutput(t *testing.T) {
	fakeOperatorCommand(t, fmt.Sprintf(`#!/bin/sh
case "$*" in
 *'get deployment'*) echo 'credential-canary'; exit 1;;
 *) /usr/bin/head -c %d /dev/zero;;
esac
`, auditLimit+1))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	output := captureStderr(t, func() { loginDNSDiagnostics(ctx, t.TempDir()) })
	if !strings.Contains(output, "CoreDNS readiness unavailable") ||
		strings.Count(output, "result=query-unavailable") != 4 ||
		len(output) > 1500 {
		t.Fatalf("bounded diagnostics: %q", output)
	}
}
