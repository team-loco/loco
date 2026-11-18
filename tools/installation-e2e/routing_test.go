package main

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestWorkerDNSRejectsMissingOrInvalidLoadBalancerAddress(t *testing.T) {
	for _, address := range []string{"", "not-an-address"} {
		t.Run(address, func(t *testing.T) {
			// Only the address lookup succeeds, and each lookup reports itself on a FIFO.
			fakeCommand(t, kubectlExecutable, `#!/bin/sh
[ "$4" = services ] || exit 1
printf '%s' "$FIXTURE_LB_ADDRESS"
echo >"$FIXTURE_LOOKUPS"
`)
			t.Setenv("FIXTURE_LB_ADDRESS", address)
			fifo := filepath.Join(t.TempDir(), "lookups")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FIXTURE_LOOKUPS", fifo)
			lookups, err := os.OpenFile(fifo, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer lookups.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() {
				// A second lookup means the first address was read and rejected.
				scanner := bufio.NewScanner(lookups)
				for range 2 {
					if !scanner.Scan() {
						return
					}
				}
				cancel()
			}()
			if err := configureGatewayDNS(ctx, true); !errors.Is(err, errGatewayAddress) {
				t.Fatalf("invalid LoadBalancer address accepted: %v", err)
			}
		})
	}
}

func TestGatewayDNSWaitsForMatchingAnswers(t *testing.T) {
	for name, worker := range map[string]bool{"control-plane": false, "worker": true} {
		t.Run(name, func(t *testing.T) {
			queries := useGatewayTestDNS(t, true, nil)
			fakeGatewayDNSCommands(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := configureGatewayDNS(ctx, worker); err != nil {
				t.Fatal(err)
			}
			if queries.Load() < 3 {
				t.Fatal("DNS configuration returned before all names resolved only to the Gateway address")
			}
		})
	}
}

func TestGatewayDNSPersistentMismatchIsBounded(t *testing.T) {
	answered := make(chan struct{}, 2)
	useGatewayTestDNS(t, false, answered)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		// A second answer means the wait rejected the first and polled again.
		<-answered
		<-answered
		cancel()
	}()
	err := waitGatewayDNS(ctx, []string{"ingest.installation.test"}, "192.0.2.10")
	if !errors.Is(err, errDNSAnswer) || !errors.Is(err, context.Canceled) {
		t.Fatalf("persistent DNS mismatch was accepted: %v", err)
	}
}

func fakeGatewayDNSCommands(t *testing.T) {
	t.Helper()
	fakeCommand(t, kubectlExecutable, `#!/bin/sh
if [ "$3" = get ] && [ "$4" = services ]; then
  printf '192.0.2.10'
elif [ "$3" = get ] && [ "$4" = configmap ]; then
  printf '.:53 {\n    hosts {\n    }\n    kubernetes cluster.local\n}\n'
fi
`)
}

func useGatewayTestDNS(t *testing.T, converge bool, answered chan<- struct{}) *atomic.Int32 {
	t.Helper()
	var listener net.ListenConfig
	conn, err := listener.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		packet := make([]byte, 512)
		for {
			n, peer, readErr := conn.ReadFrom(packet)
			if readErr != nil {
				if !errors.Is(readErr, net.ErrClosed) {
					t.Error(readErr)
				}
				return
			}
			var request dnsmessage.Message
			if unpackErr := request.Unpack(packet[:n]); unpackErr != nil {
				t.Error(unpackErr)
				return
			}
			reply := dnsmessage.Message{
				ID: request.ID, Response: true, Authoritative: true,
				Questions: request.Questions,
			}
			for _, question := range request.Questions {
				if !strings.HasSuffix(question.Name.String(), ".installation.test.") {
					reply.RCode = dnsmessage.RCodeNameError
					continue
				}
				address := [4]byte{192, 0, 2, 10}
				if question.Name.String() == "ingest.installation.test." ||
					question.Name.String() == "ready.apps.installation.test." {
					count := queries.Add(1)
					if converge && count == 1 {
						reply.RCode = dnsmessage.RCodeNameError
						continue
					}
					if !converge || count == 2 {
						address[3] = 20
					}
				}
				answer := dnsmessage.Resource{
					Header: dnsmessage.ResourceHeader{
						Name:  question.Name,
						Type:  dnsmessage.TypeA,
						Class: dnsmessage.ClassINET,
					},
					Body: &dnsmessage.AResource{A: address},
				}
				if address[3] == 20 {
					matching := answer
					matching.Body = &dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}}
					reply.Answers = append(reply.Answers, matching)
				}
				reply.Answers = append(reply.Answers, answer)
			}
			response, packErr := reply.Pack()
			if packErr != nil {
				t.Error(packErr)
				return
			}
			if _, writeErr := conn.WriteTo(response, peer); writeErr != nil && !errors.Is(writeErr, net.ErrClosed) {
				t.Error(writeErr)
				return
			}
			if answered != nil {
				select {
				case answered <- struct{}{}:
				default:
				}
			}
		}
	}()
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, conn.LocalAddr().String())
		},
	}
	t.Cleanup(func() {
		net.DefaultResolver = original
		if closeErr := conn.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		<-done
	})
	return &queries
}
