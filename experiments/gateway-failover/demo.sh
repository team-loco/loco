#!/bin/bash
# The four scenarios. Run ./setup.sh first.
set -uo pipefail
cd "$(dirname "$0")"
hr() { printf '\n\033[1m%s\033[0m\n' "$1"; }

hr "1. BASELINE -- both regions healthy"
echo "   expect: every request served by eu-west-1"
./hit.sh eu 5

hr "2. REGIONAL FAILURE -- eu-west-1 app scaled to zero"
kubectl --context kind-fo-eu scale deploy/demo-app --replicas=0 >/dev/null
kubectl --context kind-fo-eu wait --for=delete pod -l app=demo-app --timeout=90s >/dev/null 2>&1
sleep 5
echo "   expect: 200s, served by us-east-1, forwarded-by eu-west-1"
./hit.sh eu 6

hr "3. LOOP GUARD -- eu still down, request arrives already stamped by a peer"
echo "   expect: 503 locally, NOT a second hop back to us-east-1"
./hit.sh eu 4 "x-loco-forwarded-by: us-east-1"

hr "4. RECOVERY -- eu-west-1 scaled back up"
kubectl --context kind-fo-eu scale deploy/demo-app --replicas=2 >/dev/null
kubectl --context kind-fo-eu wait --for=condition=available deploy/demo-app --timeout=120s >/dev/null
sleep 8
echo "   expect: traffic returns to eu-west-1 with no manual intervention"
./hit.sh eu 5
echo
