#!/bin/bash
# hit.sh <eu|us> [count] [extra-header]
# Sends N requests at a region's gateway and reports which region actually served each.
REGION=${1:-eu}; COUNT=${2:-10}; EXTRA=${3:-}
case "$REGION" in
  eu) IP=$(docker inspect fo-eu-control-plane --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
      NP=$(kubectl --context kind-fo-eu get svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=eg -o jsonpath='{.items[0].spec.ports[?(@.port==80)].nodePort}');;
  us) IP=$(docker inspect fo-us-control-plane --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
      NP=$(kubectl --context kind-fo-us get svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=eg -o jsonpath='{.items[0].spec.ports[?(@.port==80)].nodePort}');;
esac
HDR=""; [ -n "$EXTRA" ] && HDR="-H \"$EXTRA\""
docker run --rm --network kind curlimages/curl:8.11.1 sh -c "
for i in \$(seq 1 $COUNT); do
  out=\$(curl -s -m 5 -w '\nHTTPSTATUS:%{http_code}' -H 'Host: app.demo.local' $HDR http://$IP:$NP/ 2>/dev/null)
  code=\$(echo \"\$out\" | sed -n 's/^HTTPSTATUS://p')
  name=\$(echo \"\$out\" | sed -n 's/^Name: //p')
  fwd=\$(echo \"\$out\"  | sed -n 's/^X-Loco-Forwarded-By: //p')
  printf 'status=%-3s served-by=%-12s forwarded-by=%s\n' \"\$code\" \"\${name:--}\" \"\${fwd:--}\"
done"
