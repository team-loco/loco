package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	dnsDiagnosticPrefix = "installation-e2e DNS: "
	dnsQueryTimeout     = 5 * time.Second
)

// Only fixture names and fixed DNS result categories cross the CLI output boundary.
func reportLoginDNSFailure(output []byte) {
	text := string(output)
	for _, host := range []string{fixtureAPIHost, fixtureIssuerHost} {
		if !strings.Contains(text, "lookup "+host+":") && !strings.Contains(text, "lookup "+host+" on ") {
			continue
		}
		for _, result := range []string{"no such host", "server misbehaving"} {
			if strings.Contains(text, ": "+result) {
				fmt.Fprintf(os.Stderr, "%sfailed lookup host=%s result=%s\n", dnsDiagnosticPrefix, host, result)
			}
		}
	}
}

func loginDNSDiagnostics(ctx context.Context, dir string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureDiagnosticTimeout)
	defer cancel()
	query := func(args ...string) ([]byte, bool) {
		queryCtx, cancel := context.WithTimeout(ctx, dnsQueryTimeout)
		defer cancel()
		cmd := exec.CommandContext(queryCtx, "kubectl", args...)
		cmd.Dir = dir
		var output boundedOutput
		cmd.Stdout, cmd.Stderr = &output, io.Discard
		err := cmd.Run()
		return output.Bytes(), err == nil && !output.exceeded
	}
	data, ok := query("-n", "kube-system", "get", "deployment", "coredns", "-o", "json")
	var deployment struct {
		Spec struct {
			Replicas int32 `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas     int32 `json:"readyReplicas"`
			AvailableReplicas int32 `json:"availableReplicas"`
			UpdatedReplicas   int32 `json:"updatedReplicas"`
		} `json:"status"`
	}
	if !ok || json.Unmarshal(data, &deployment) != nil {
		fmt.Fprintln(os.Stderr, dnsDiagnosticPrefix+"CoreDNS readiness unavailable")
	} else {
		fmt.Fprintf(
			os.Stderr,
			"%sCoreDNS desired=%d ready=%d available=%d updated=%d\n",
			dnsDiagnosticPrefix,
			deployment.Spec.Replicas,
			deployment.Status.ReadyReplicas,
			deployment.Status.AvailableReplicas,
			deployment.Status.UpdatedReplicas,
		)
	}
	for _, host := range []string{fixtureAPIHost, fixtureIssuerHost} {
		for _, family := range []string{"A", "AAAA"} {
			data, ok := query("-n", fixtureNamespace, "exec", "operator", "--", "nslookup", "-type="+family, host)
			fmt.Fprintf(os.Stderr, "%snamespace=%s pod=operator host=%s type=%s result=%s\n",
				dnsDiagnosticPrefix, fixtureNamespace, host, family, dnsQueryResult(data, ok))
		}
	}
}

func dnsQueryResult(data []byte, success bool) string {
	// Parse answers only; nslookup's server address includes a port and is excluded.
	var addresses []string
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "Address:" {
			if address, err := netip.ParseAddr(fields[1]); err == nil && len(addresses) < 8 {
				addresses = append(addresses, address.String())
			}
		}
	}
	for _, result := range []string{"NXDOMAIN", "SERVFAIL", "REFUSED"} {
		if strings.Contains(string(data), result) {
			return result
		}
	}
	if !success {
		return "query-unavailable"
	}
	if len(addresses) == 0 {
		return "no-addresses"
	}
	return strings.Join(addresses, ",")
}
