package main

import (
	"context"
	"errors"
	"testing"
	"time"
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
