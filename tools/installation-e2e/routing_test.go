package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorkerDNSFindsLoadBalancerInControllerNamespace(t *testing.T) {
	fakeOperatorCommand(t, `#!/bin/sh
case "$*" in
 *"get services"*)
  # The worker-owned Service lives in the shared controller's namespace.
  case " $* " in
   *" --all-namespaces "*) ;;
   *) exit 1 ;;
  esac
  case " $* " in
   *" -l gateway.envoyproxy.io/owning-gateway-namespace=loco-staging "*) ;;
   *) exit 1 ;;
  esac
  printf '192.0.2.10'
  ;;
 *"get configmap coredns"*)
  printf '    hosts {\n    }\n    kubernetes cluster.local\n'
  ;;
esac
`)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := configureGatewayDNS(ctx, workerNamespace, true); err != nil {
		t.Fatalf("worker LoadBalancer in shared controller namespace was not discovered: %v", err)
	}
}

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
