package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

var errTLS12Accepted = errors.New("gateway accepted a TLS 1.2 handshake")

// Upgrade after recovery so the HTTP recovery fixture can finish before its API URL changes.
func verifyTLSMinimum(ctx context.Context, hostname string) (result error) {
	roots, err := fixtureRoots(ctx)
	if err != nil {
		return err
	}
	data, err := kubectl(ctx, "-n", "flux-system", "get", "configmap", "loco-control-plane-values", "-o", "json")
	if err != nil {
		return err
	}
	var values struct {
		Data map[string]string `json:"data"`
	}
	if err = json.Unmarshal(data, &values); err != nil {
		return err
	}
	source := strings.Replace(
		values.Data["values.yaml"],
		"certManager: {enabled: false}",
		`certManager: {enabled: true, issuer: {create: false, name: installation-ca, kind: ClusterIssuer}}
gateway:
  tls: {minVersion: "1.3"}
`,
		1,
	)
	for _, host := range []string{fixtureAPIHost, fixtureWebHost, fixtureIngestHost} {
		source = strings.ReplaceAll(source, "http://"+host, "https://"+host)
	}
	patch, err := json.Marshal(map[string]any{fluxData: map[string]string{"values.yaml": source}})
	if err != nil {
		return err
	}
	if _, err = kubectl(
		ctx,
		"-n",
		"flux-system",
		"patch",
		"configmap",
		"loco-control-plane-values",
		"--type=merge",
		"--patch",
		string(patch),
	); err != nil {
		return err
	}
	// Suspend the fixture config source while testing an explicit operator values change.
	if _, err = command(ctx, "", nil, "flux", "suspend", "kustomization", "loco-cluster-config"); err != nil {
		return err
	}
	if _, err = command(
		ctx,
		"",
		nil,
		"flux",
		"reconcile",
		"helmrelease",
		"loco-control-plane",
		"--timeout=10m",
	); err != nil {
		return err
	}
	if err := configureGatewayDNS(ctx, false); err != nil {
		return err
	}
	for _, target := range []struct{ address, serverName string }{
		{fixtureAPIHost + ":443", fixtureAPIHost},
		{fixtureWebHost + ":443", fixtureWebHost},
		{fixtureIngestHost + ":443", fixtureIngestHost},
		{hostname + ":443", hostname},
		// Reach the worker's platform listener without depending on public DNS for loco.build.
		{hostname + ":443", "probe.loco.build"},
	} {
		if err := waitTLS13(ctx, target.address, target.serverName, roots); err != nil {
			return fmt.Errorf("TLS minimum for %s: %w", target.serverName, err)
		}
	}
	return nil
}

func waitTLS13(ctx context.Context, address, serverName string, roots *x509.CertPool) error {
	ctx, cancel := context.WithTimeout(ctx, httpsReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		err := requireTLS13(ctx, address, serverName, roots)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func requireTLS13(ctx context.Context, address, serverName string, roots *x509.CertPool) error {
	for _, version := range []uint16{tls.VersionTLS13, tls.VersionTLS12} {
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: time.Second}, Config: &tls.Config{
			RootCAs: roots, ServerName: serverName, MinVersion: version, MaxVersion: version,
		}}
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			// A network or certificate error does not prove that TLS 1.2 was rejected.
			if version == tls.VersionTLS12 &&
				strings.Contains(err.Error(), "remote error: tls: protocol version not supported") {
				return nil
			}
			return err
		}
		if err := connection.Close(); err != nil {
			return err
		}
		if version == tls.VersionTLS12 {
			return errTLS12Accepted
		}
	}
	return nil
}
