package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

var (
	errDNSAnswer      = errors.New("fixture Gateway DNS does not resolve to its LoadBalancer address")
	errDNSZone        = errors.New("incomplete fixture DNS zone")
	errGatewayAddress = errors.New("fixture Gateway has no real LoadBalancer address")
	errRegistryDNS    = errors.New("cluster DNS does not resolve the fixture registry host to its service address")
)

const (
	fixtureAPIHost      = "api.installation.test"
	fixtureWebHost      = "console.installation.test"
	fixtureIngestHost   = "ingest.installation.test"
	fixtureAppsDomain   = "apps.installation.test"
	gatewayReadyTimeout = 2 * time.Minute
)

func (f *fixture) startLoadBalancer(ctx context.Context) error {
	image, err := f.pin(ctx, "dev/compose.cloud-provider-kind.yaml", `.services."cloud-provider-kind".image`)
	if err != nil {
		return err
	}
	own, err := command(
		ctx,
		f.root,
		nil,
		"docker",
		"compose",
		"-p",
		f.name,
		"-f",
		"dev/compose.cloud-provider-kind.yaml",
		"ps",
		"-q",
	)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(own))) > 0 {
		f.cloudManaged = true
		return nil
	}
	running, err := command(ctx, f.root, nil, "docker", "ps", "-q", "--filter", "ancestor="+image)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(running))) > 0 {
		return nil
	}
	if _, checkErr := command(
		ctx,
		f.root,
		nil,
		"docker",
		"compose",
		"-p",
		f.name,
		"-f",
		"dev/compose.cloud-provider-kind.yaml",
		"up",
		"-d",
	); checkErr != nil {
		return checkErr
	}
	f.cloudManaged = true
	return nil
}

func configureGatewayDNS(ctx context.Context, worker bool) error {
	namespace := controlNamespace
	deadline, cancel := context.WithTimeout(ctx, gatewayReadyTimeout)
	defer cancel()
	var address string
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	gateway := "loco-control-plane"
	if worker {
		gateway = "eg"
	}
	for {
		output, err := kubectl(
			deadline,
			"-n",
			namespace,
			"get",
			"services",
			"-l",
			"gateway.envoyproxy.io/owning-gateway-namespace="+namespace+",gateway.envoyproxy.io/owning-gateway-name="+gateway,
			"-o",
			"jsonpath={.items[0].status.loadBalancer.ingress[0].ip}",
		)
		if err == nil {
			address = strings.TrimSpace(string(output))
			if _, parseErr := netip.ParseAddr(address); parseErr == nil {
				break
			}
		}
		select {
		case <-deadline.Done():
			return errGatewayAddress
		case <-ticker.C:
		}
	}
	output, err := kubectl(ctx, "-n", "kube-system", "get", "configmap", "coredns", "-o", "jsonpath={.data.Corefile}")
	if err != nil {
		return err
	}
	core := string(output)
	names := []string{fixtureAPIHost, fixtureWebHost, fixtureIngestHost}
	if worker {
		names = []string{"ready." + fixtureAppsDomain}
		// The zone is separate from the control-plane hosts. Product verification chooses
		// its own unique hostname under this wildcard, so it uses the same DNS as applications.
		begin := "    # installation-apps-start"
		end := "    # installation-apps-end"
		if before, rest, found := strings.Cut(core, begin); found {
			_, after, ok := strings.Cut(rest, end)
			if !ok {
				return errDNSZone
			}
			core = before + after
		}
		zone := fmt.Sprintf(
			"%s\n    template IN A %s {\n        answer \"{{ .Name }} 30 IN A %s\"\n    }\n%s\n",
			begin,
			fixtureAppsDomain,
			address,
			end,
		)
		core = strings.Replace(core, "    kubernetes cluster.local", zone+"    kubernetes cluster.local", 1)
	} else {
		var lines []string
		for line := range strings.SplitSeq(core, "\n") {
			skip := false
			for _, host := range names {
				if strings.Contains(line, host) {
					skip = true
				}
			}
			if !skip {
				lines = append(lines, line)
			}
		}
		core = strings.Join(lines, "\n")
		core = strings.Replace(
			core,
			"    hosts {\n",
			"    hosts {\n        "+address+" "+strings.Join(names, " ")+"\n",
			1,
		)
	}
	patch, err := json.Marshal(map[string]any{fluxData: map[string]string{"Corefile": core}})
	if err != nil {
		return err
	}
	if _, checkErr := kubectl(
		ctx,
		"-n",
		"kube-system",
		"patch",
		"configmap",
		"coredns",
		"--type=merge",
		"--patch",
		string(patch),
	); checkErr != nil {
		return checkErr
	}
	if checkErr := restartClusterDNS(ctx); checkErr != nil {
		return checkErr
	}
	// Deployment readiness does not prove the operator can resolve the new zone.
	return waitGatewayDNS(ctx, names, address)
}

// restartClusterDNS makes CoreDNS serve the current Corefile. Running pods pick up
// an edit only after the kubelet syncs the ConfigMap and the reload plugin polls it,
// which takes over a minute.
func restartClusterDNS(ctx context.Context) error {
	if _, checkErr := kubectl(ctx, "-n", "kube-system", "rollout", "restart", "deployment/coredns"); checkErr != nil {
		return checkErr
	}
	_, err := kubectl(
		ctx,
		"-n",
		"kube-system",
		"rollout",
		"status",
		"deployment/coredns",
		"--timeout="+fixtureReadyTimeout,
	)
	return err
}

// waitRegistryDNS waits until pods resolve the fixture registry host to its service
// address. Until CoreDNS serves the hosts entry, the .localhost name resolves to loopback.
func waitRegistryDNS(ctx context.Context, address string) error {
	host, _, err := net.SplitHostPort(registryHost)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, gatewayReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// The postgres fixture image ships nslookup and uses the cluster DNS.
		output, lookupErr := kubectl(ctx, "-n", fixtureNamespace, "exec", "deploy/postgres", "--",
			"nslookup", "-type=a", host)
		if lookupErr == nil && resolvesTo(string(output), address) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(errRegistryDNS, ctx.Err())
		case <-ticker.C:
		}
	}
}

// resolvesTo reports whether nslookup output answers with address. The server's own
// Address line carries a port, so it never matches.
func resolvesTo(output, address string) bool {
	for line := range strings.SplitSeq(output, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "Address:" && fields[1] == address {
			return true
		}
	}
	return false
}

func waitGatewayDNS(ctx context.Context, names []string, address string) error {
	ctx, cancel := context.WithTimeout(ctx, gatewayReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		ready := true
		for _, name := range names {
			answers, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", name)
			if err != nil || len(answers) == 0 {
				ready = false
				break
			}
			for _, answer := range answers {
				if answer.String() != address {
					ready = false
					break
				}
			}
			if !ready {
				break
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(errDNSAnswer, ctx.Err())
		case <-ticker.C:
		}
	}
}
