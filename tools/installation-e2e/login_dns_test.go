package main

import (
	"strings"
	"testing"
)

func TestDNSQueryResult(t *testing.T) {
	for _, tc := range []struct {
		output  string
		success bool
		want    string
	}{
		{"Address: fd00::4\n", true, "fd00::4"},
		{"REFUSED", false, "REFUSED"},
		{"dns-private-value", true, "no-addresses"},
		{strings.Repeat("Address: 172.18.0.4\n", 10000), true, strings.TrimSuffix(strings.Repeat("172.18.0.4,", 8), ",")},
	} {
		if got := dnsQueryResult([]byte(tc.output), tc.success); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestLoginDNSFailureProjectsFixtureNames(t *testing.T) {
	output := captureOutput(t, func() {
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
