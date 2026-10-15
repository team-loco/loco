#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
RUN_HASH=$(printf '%s' "$ROOT_DIR" | cksum | awk '{print $1}')
CLUSTER_NAME="loco-certificates-${E2E_RUN_ID:-$RUN_HASH}"
RUN_DIR="$ROOT_DIR/e2e/runs/$CLUSTER_NAME"
KEEP=false
if [[ ${1:-} == --no-teardown ]]; then
    KEEP=true
fi
mkdir -p "$RUN_DIR"
export KUBECONFIG="$RUN_DIR/kubeconfig"
CERT_MANAGER_VERSION=$(awk '/chart: jetstack\/cert-manager/ { found=1; next } found && $1 == "version:" { print $2; exit }' "$ROOT_DIR/helmfile.yaml.gotmpl")
TIMEOUT=300s

cleanup() {
    local result=$?
    if [[ $result -ne 0 ]]; then
        kubectl get pods,certificates,issuers,clusterissuers -A -o wide >"$RUN_DIR/diagnostics.txt" 2>&1 || true
        kubectl get events -A --sort-by=.lastTimestamp >>"$RUN_DIR/diagnostics.txt" 2>&1 || true
        kubectl -n cert-manager logs deployment/cert-manager >>"$RUN_DIR/diagnostics.txt" 2>&1 || true
    fi
    if [[ $KEEP == false ]]; then
        kind delete cluster --name "$CLUSTER_NAME"
    fi
    exit "$result"
}
trap cleanup EXIT

if ! kind get clusters | rg -x "$CLUSTER_NAME" >/dev/null; then
    kind create cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG" --wait "$TIMEOUT"
fi
helm upgrade --install cert-manager cert-manager --repo https://charts.jetstack.io \
    --version "$CERT_MANAGER_VERSION" --namespace cert-manager --create-namespace \
    --set crds.enabled=true --wait --timeout "$TIMEOUT"
kubectl create namespace loco-certificates --dry-run=client -o yaml | kubectl apply -f -

render_certificate() {
    local release=$1
    shift
    helm template "$release" "$ROOT_DIR/charts/loco-core" -n loco-certificates \
        --set gateway-helm.enabled=false --set agent.image.tag=e2e \
        --set agent.clusterName=certificates --set agent.clusterRegion=test --set agent.clusterTier=production \
        --set certManager.issuer.name=loco-certificates-local \
        --set certManager.issuer.type=selfSigned \
        --set-json 'global.domain.apps=["apps.example.test"]' \
        --set global.domain.platform=platform.example.test \
        --show-only templates/cm-cert.yaml "$@"
}

helm template local "$ROOT_DIR/charts/loco-core" -n loco-certificates \
    --set gateway-helm.enabled=false --set agent.image.tag=e2e \
        --set agent.clusterName=certificates --set agent.clusterRegion=test --set agent.clusterTier=production \
    --set certManager.issuer.name=loco-certificates-local --set certManager.issuer.type=selfSigned \
    --show-only templates/cm-issuer.yaml | kubectl apply -f -
render_certificate local | kubectl apply -f -
kubectl -n loco-certificates wait certificate/local-cert --for=condition=Ready --timeout="$TIMEOUT"
kubectl -n loco-certificates get secret local-tls -o jsonpath='{.data.tls\.crt}' | base64 -d >"$RUN_DIR/local.pem"
openssl x509 -in "$RUN_DIR/local.pem" -noout -checkhost probe.apps.example.test

kubectl -n loco-certificates apply -f - <<'YAML'
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: existing-local
spec:
  selfSigned: {}
YAML
render_certificate existing --set certManager.issuer.create=false \
    --set certManager.issuer.name=existing-local --set certManager.issuer.kind=Issuer | kubectl apply -f -
kubectl -n loco-certificates wait certificate/existing-cert --for=condition=Ready --timeout="$TIMEOUT"
kubectl -n loco-certificates get secret existing-tls -o jsonpath='{.data.tls\.crt}' | base64 -d >"$RUN_DIR/existing.pem"
openssl x509 -in "$RUN_DIR/existing.pem" -noout -checkhost probe.platform.example.test
printf 'Chart-issued and existing-issuer TLS certificates are ready.\n'
