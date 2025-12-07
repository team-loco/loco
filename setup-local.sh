#!/bin/bash

set -e

echo "Setting up Loco for local development..."

# Check if minikube is installed
if ! command -v minikube &> /dev/null; then
    echo "Installing minikube..."
    # For macOS ARM64
    curl -LO https://storage.googleapis.com/minikube/releases/latest/minikube-darwin-arm64
    sudo install minikube-darwin-arm64 /usr/local/bin/minikube
    rm minikube-darwin-arm64
fi

# Start minikube if not running
if ! minikube status | grep -q "Running"; then
    echo "Starting minikube..."
    minikube start
fi

# Check if helm is installed
if ! command -v helm &> /dev/null; then
    echo "Installing helm..."
    # For macOS ARM64
    curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
    chmod 700 get_helm.sh
    ./get_helm.sh
    rm get_helm.sh
fi

# Add helm repos
# echo "Adding helm repos..."
# helm repo add cilium https://helm.cilium.io/
# helm repo add jetstack https://charts.jetstack.io
# helm repo add altinity https://helm.altinity.com
# helm repo add grafana https://grafana.github.io/helm-charts
# helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
# helm repo update

# # Install Cilium
# echo "Installing Cilium..."
# helm upgrade --install cilium cilium/cilium --namespace kube-system -f kube/values/cilium.yml

echo "Installing Envoy Gateway + Gateway Crds..."
kubectl apply --server-side=true -f https://github.com/envoyproxy/gateway/releases/download/v1.6.0/envoy-gateway-crds.yaml
helm upgrade eg oci://docker.io/envoyproxy/gateway-helm -n envoy-gateway-system --create-namespace -i --values kube/values/gateway.yml

# Install cert-manager
echo "Installing cert-manager..."
helm upgrade --install cert-manager jetstack/cert-manager --namespace cert-manager --create-namespace --set crds.enabled=true -i


# below needs to be replaced with cf-token.
kubectl create secret generic cloudflare-api-token-secret \
  --from-literal=api-token=<dummy-token-here> \
  -n cert-manager

# Install ClickStack
# echo "Installing ClickStack for observability..."

# echo "Installing local path provisioner for MongoDB..."
# kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/master/deploy/local-path-storage.yaml

# SECRET=$(openssl rand -base64 16)

# kubectl create secret generic hyperdx-secret --from-literal=HYPERDX_API_KEY="$SECRET" -n observability

# helm install clickstack hyperdx/hdx-oss-v2 --values kube/values/clickhouse.yml --set hyperdx.apiKey="$SECRET" -n observability --create-namespace

# echo "Creating OpenTelemetry ConfigMap..."
# kubectl create configmap otel-config-vars \
#  --from-literal=OTEL_COLLECTOR_ENDPOINT="http://clickstack-hdx-oss-v2-otel-collector.observability.svc.cluster.local:4318" \
#  -n observability --dry-run=client -o yaml | kubectl apply -f -
# echo "Creating clickhouse"
# helm upgrade --install clickhouse altinity/clickhouse --namespace observability --create-namespace --version 0.3.4 --set clickhouse.defaultUser.allowExternalAccess=true

# echo "Installing OpenTelemetry Operator..."
# helm install opentelemetry-operator open-telemetry/opentelemetry-operator --create-namespace --namespace opentelemetry-operator-system

# echo "Installing OpenTelemetry collectors..."
# helm install clickhouse-otel-ds open-telemetry/opentelemetry-collector --values kube/values/otel-daemonset.yml --namespace observability
# helm install clickhouse-otel-deploy open-telemetry/opentelemetry-collector --values kube/values/otel-deployment.yml --namespace observability

# echo "Installing grafana"
# helm install grafana grafana/grafana --values kube/values/grafana.yml --namespace observability

# create loco namespace
kubectl apply -f kube/loco/namespace.yml
# Create self-signed issuer

echo "Creating self-signed issuer..."
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}
EOF

# Create certificate for localhost
echo "Creating certificate for localhost..."
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: loco-tls
  namespace: loco-system
spec:
  secretName: loco-tls
  issuerRef:
    name: selfsigned-issuer
    kind: ClusterIssuer
  commonName: localhost
  dnsNames:
  - localhost
EOF

# Apply base resources
# echo "Applying base resources..."
# kubectl apply -f kube/gateway/gateway.yml
# kubectl apply -f kube/loco/routes.yml
# kubectl apply -f kube/loco/rbac.yml

# Build and load image
# echo "Building loco image..."
# docker build -t loco-api:latest -f api/Dockerfile .
# minikube image load loco-api:latest

# Create secrets with dummy values for local dev
echo "Creating secrets..."
kubectl create secret generic env-config \
  --from-literal=GITLAB_PAT=dummy \
  --from-literal=GITLAB_PROJECT_ID=dummy \
  --from-literal=GITLAB_TOKEN_NAME=dummy \
  --from-literal=GH_OAUTH_CLIENT_ID=dummy \
  --from-literal=GITLAB_URL=dummy \
  --from-literal=GITLAB_REGISTRY_URL=dummy \
  --from-literal=GITLAB_DEPLOY_TOKEN_NAME=dummy \
  --from-literal=APP_ENV=DEVELOPMENT \
  --from-literal=LOG_LEVEL=-4 \
  --from-literal=PORT=:8000 \
  --from-literal=GH_OAUTH_CLIENT_SECRET=dummy \
  --from-literal=GH_OAUTH_REDIRECT_URL=http://localhost:8000/api/v1/oauth/github/callback \
  --from-literal=GH_OAUTH_STATE=dummy \
  -n loco-system --dry-run=client -o yaml | kubectl apply -f -

# # Update deployment to use local image
# kubectl apply -f kube/loco/service.yml
# echo "Updating deployment image..."
# kubectl set image -n loco-system deployment/loco-api loco-api=loco-api:latest
# kubectl patch deployment loco-api -n loco-system --type='json' -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/imagePullPolicy", "value": "Never"}]'


# echo "\n\nSetup complete!"
# echo "Note: the secrets installed are just dummy."
# echo "To access the application:"
# echo "1. Run 'minikube tunnel' in a separate terminal to expose the LoadBalancer."
# echo "2. Access the app at http://localhost"
# echo "3. To check status: kubectl get pods -n loco-system"
# echo "4. To view logs: kubectl logs -n loco-system deployment/loco-api"
