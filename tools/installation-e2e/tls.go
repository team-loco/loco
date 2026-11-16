package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
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
	data, err := os.ReadFile(clientFile)
	if err != nil {
		return err
	}
	source := strings.Replace(string(data), "controlPlane:\n", "controlPlane:\n  tls: {minVersion: \"1.3\"}\n", 1)
	for _, host := range []string{fixtureAPIHost, fixtureWebHost, fixtureIngestHost} {
		source = strings.ReplaceAll(source, "http://"+host, "https://"+host)
	}
	// The original installation file is mounted from a read-only ConfigMap.
	file, err := os.CreateTemp("", "installation-tls-*.yaml")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.Remove(file.Name())) }()
	if _, err := file.WriteString(source); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	if _, err := locoadm(ctx, "apply", "--file", file.Name()); err != nil {
		return err
	}
	if err := configureGatewayDNS(ctx, controlNamespace, false); err != nil {
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
