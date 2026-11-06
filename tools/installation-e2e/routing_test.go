package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestWorkerDNSRejectsMissingOrInvalidLoadBalancerAddress(t *testing.T) {
	for _, address := range []string{"", "not-an-address"} {
		t.Run(address, func(t *testing.T) {
			fakeOperatorCommand(t, "#!/bin/sh\nprintf '%s' \"$FIXTURE_LB_ADDRESS\"\n")
			t.Setenv("FIXTURE_LB_ADDRESS", address)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			if err := configureGatewayDNS(ctx, workerNamespace, true); !errors.Is(err, errGatewayAddress) {
				t.Fatalf("invalid LoadBalancer address accepted: %v", err)
			}
		})
	}
}

func TestGatewayDNSWaitsForMatchingAnswers(t *testing.T) {
	for name, worker := range map[string]bool{"control-plane": false, "worker": true} {
		t.Run(name, func(t *testing.T) {
			queries := useGatewayTestDNS(t, true)
			fakeGatewayDNSCommands(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := configureGatewayDNS(ctx, controlNamespace, worker); err != nil {
				t.Fatal(err)
			}
			if queries.Load() < 3 {
				t.Fatal("DNS configuration returned before all names resolved only to the Gateway address")
			}
		})
	}
}

func TestGatewayDNSPersistentMismatchIsBounded(t *testing.T) {
	useGatewayTestDNS(t, false)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := waitGatewayDNS(ctx, []string{"ingest.installation.test"}, "192.0.2.10")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("persistent DNS mismatch was accepted: %v", err)
	}
}

func fakeGatewayDNSCommands(t *testing.T) {
	t.Helper()
	fakeOperatorCommand(t, `#!/bin/sh
if [ "$3" = get ] && [ "$4" = services ]; then
  printf '192.0.2.10'
elif [ "$3" = get ] && [ "$4" = configmap ]; then
  printf '.:53 {\n    hosts {\n    }\n    kubernetes cluster.local\n}\n'
fi
`)
}

func useGatewayTestDNS(t *testing.T, converge bool) *atomic.Int32 {
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
