#!/bin/bash
# Cross-region gateway-to-gateway failover, two kind clusters, no service mesh.
#
# Builds two "regions" (fo-eu, fo-us). Each runs Envoy Gateway and a demo app.
# Each region's gateway carries the *other* region's public gateway as a
# priority-1 fallback backend, so a regional outage is absorbed at L7 without
# any pod-network connectivity between clusters.
set -euo pipefail
cd "$(dirname "$0")"

EU=fo-eu; US=fo-us
EU_REGION=eu-west-1; US_REGION=us-east-1

echo "==> creating clusters"
for c in $EU $US; do
  kind get clusters 2>/dev/null | grep -qx "$c" || kind create cluster --config "kind-${c#fo-}.yaml"
done

EU_IP=$(docker inspect ${EU}-control-plane --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
US_IP=$(docker inspect ${US}-control-plane --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
echo "    $EU -> $EU_IP    $US -> $US_IP"

echo "==> installing Envoy Gateway (with the Backend extension API enabled)"
# enableBackend is OFF by default; without it the Backend CRD exists but every
# route referencing one fails with "Backend is disabled in Envoy Gateway configuration".
for c in $EU $US; do
  helm upgrade --install eg oci://docker.io/envoyproxy/gateway-helm \
    -n envoy-gateway-system --create-namespace --kube-context "kind-$c" \
    --set config.envoyGateway.extensionApis.enableBackend=true \
    --wait --timeout 8m >/dev/null
done

echo "==> peer DNS"
# The fallback Backend MUST use an fqdn endpoint (see README: ip endpoints silently
# degrade to a weighted split). In production this is the peer region's real public
# hostname; here CoreDNS maps it to the peer kind node.
add_peer_dns() {
  local ctx=$1 name=$2 ip=$3
  local f=/tmp/Corefile.$ctx
  kubectl --context "$ctx" get cm coredns -n kube-system -o jsonpath='{.data.Corefile}' > "$f"
  grep -q "$name" "$f" && return 0
  python3 - "$f" "$name" "$ip" <<'PY'
import sys
f,name,ip=sys.argv[1:4]
s=open(f).read()
open(f,"w").write(s.replace("ready\n", f"ready\n    hosts {{\n        {ip} {name}\n        fallthrough\n    }}\n",1))
PY
  kubectl --context "$ctx" create cm coredns -n kube-system --from-file=Corefile="$f" \
    --dry-run=client -o yaml | kubectl --context "$ctx" apply -f - >/dev/null 2>&1
  kubectl --context "$ctx" rollout restart deploy/coredns -n kube-system >/dev/null
}
add_peer_dns "kind-$EU" "${US_REGION}.gw.demo.local" "$US_IP"
add_peer_dns "kind-$US" "${EU_REGION}.gw.demo.local" "$EU_IP"

echo "==> gateways + apps"
for pair in "$EU:$EU_REGION" "$US:$US_REGION"; do
  c="${pair%%:*}"; r="${pair##*:}"
  kubectl --context "kind-$c" apply -f manifests/00-gateway.yaml >/dev/null
  sed "s/__REGION__/$r/" manifests/10-app.yaml | kubectl --context "kind-$c" apply -f - >/dev/null
done

echo "==> waiting for gateways to program"
for c in $EU $US; do
  kubectl --context "kind-$c" wait --for=condition=Programmed gateway/eg --timeout=5m >/dev/null
  kubectl --context "kind-$c" wait --for=condition=available deploy/demo-app --timeout=5m >/dev/null
done

nodeport() { kubectl --context "kind-$1" get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=eg \
  -o jsonpath='{.items[0].spec.ports[?(@.port==80)].nodePort}'; }
EU_NP=$(nodeport $EU); US_NP=$(nodeport $US)

echo "==> failover routing"
apply_failover() {
  sed -e "s/__PEER_HOST__/$2/" -e "s/__PEER_PORT__/$3/" -e "s/__REGION__/$4/" \
    manifests/20-failover.yaml | kubectl --context "kind-$1" apply -f - >/dev/null
}
apply_failover $EU "${US_REGION}.gw.demo.local" "$US_NP" "$EU_REGION"
apply_failover $US "${EU_REGION}.gw.demo.local" "$EU_NP" "$US_REGION"
sleep 10

cat <<EOF

Ready.

  eu-west-1 gateway  http://$EU_IP:$EU_NP   (Host: app.demo.local)
  us-east-1 gateway  http://$US_IP:$US_NP   (Host: app.demo.local)

  ./demo.sh              run the four failover scenarios
  ./hit.sh eu 10         send 10 requests at the EU gateway
  ./inspect.sh eu        show Envoy's priority levels and host health
  ./teardown.sh          delete both clusters
EOF
