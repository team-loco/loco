#!/bin/bash
set -e

CLUSTER_NAME=${1:-clickstack-test}

echo "Creating kind cluster: $CLUSTER_NAME"
kind create cluster --name "$CLUSTER_NAME" --config c.yaml

echo "Configuring CoreDNS to use 8.8.8.8..."
kubectl get configmap coredns -n kube-system -o yaml --context "kind-$CLUSTER_NAME" | \
  sed 's|forward . /etc/resolv.conf|forward . 8.8.8.8|g' | \
  kubectl apply -f - --context "kind-$CLUSTER_NAME"
kubectl rollout restart deployment coredns -n kube-system --context "kind-$CLUSTER_NAME"

echo "Installing local-path storage provisioner..."
kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/v0.0.30/deploy/local-path-storage.yaml --context "kind-$CLUSTER_NAME"
kubectl patch storageclass local-path -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}' --context "kind-$CLUSTER_NAME"

echo "Installing metrics-server..."
helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/
helm repo update
helm upgrade --install metrics-server metrics-server/metrics-server \
  --set args="{--kubelet-insecure-tls}" \
  --kube-context "kind-$CLUSTER_NAME"

echo "Installing kubernetes-dashboard..."
helm repo add kubernetes-dashboard https://kubernetes.github.io/dashboard/
helm repo update
helm upgrade --install kubernetes-dashboard kubernetes-dashboard/kubernetes-dashboard \
  --create-namespace \
  --namespace kubernetes-dashboard \
  --kube-context "kind-$CLUSTER_NAME"

echo "Deploying OpenTelemetry Demo application..."
kubectl create namespace otel-demo --context "kind-$CLUSTER_NAME" --dry-run=client -o yaml | kubectl apply -f - --context "kind-$CLUSTER_NAME"
kubectl apply --namespace otel-demo -f https://raw.githubusercontent.com/ClickHouse/opentelemetry-demo/main/kubernetes/opentelemetry-demo.yaml --context "kind-$CLUSTER_NAME"

echo "Adding ClickStack Helm repository..."
helm repo add clickstack https://clickhouse.github.io/ClickStack-helm-charts
helm repo update

echo "Installing ClickStack with ClickHouse..."
helm upgrade --install clickstack clickstack/clickstack \
  --set global.storageClassName="local-path" \
  --namespace otel-demo \
  --values values.yaml \
  --kube-context "kind-$CLUSTER_NAME"

echo "Waiting for ClickStack to be ready..."
sleep 30

echo "Applying critical fixes..."
echo "1. Fixing OTEL collector endpoint for demo apps..."
kubectl set env deployment -n otel-demo -l app.kubernetes.io/part-of=opentelemetry-demo OTEL_COLLECTOR_NAME=clickstack-otel-collector --context "kind-$CLUSTER_NAME"

echo "2. Fixing load-generator image (avoiding Docker Hub rate limits)..."
kubectl set image deployment/load-generator -n otel-demo load-generator=ghcr.io/open-telemetry/demo:2.1.3-load-generator --context "kind-$CLUSTER_NAME"

echo "3. Deploying Kubernetes metrics collector for HyperDX K8s dashboard..."
kubectl apply -f k8s-metrics-collector.yaml --context "kind-$CLUSTER_NAME"

echo ""
echo "Waiting for deployments to stabilize..."
sleep 20

echo ""
echo "Getting HyperDX ingestion API key..."
echo "You'll need to access the HyperDX UI to create an account and get the API key"
echo "Then create the secret with:"
echo "  kubectl create secret generic hyperdx-secret --from-literal=HYPERDX_API_KEY=<your-key> -n otel-demo --context kind-$CLUSTER_NAME"
echo ""
echo "After creating the secret, restart the demo pods:"
echo "  kubectl rollout restart deployment -n otel-demo -l app.kubernetes.io/part-of=opentelemetry-demo --context kind-$CLUSTER_NAME"

echo ""
echo "Cluster '$CLUSTER_NAME' is ready with ClickStack and OpenTelemetry Demo!"
echo ""
echo "To check status:"
echo "  kubectl get pods -n otel-demo --context kind-$CLUSTER_NAME"
echo ""
echo "To access HyperDX UI:"
echo "  kubectl port-forward -n otel-demo svc/clickstack-app 8080:3000 --context kind-$CLUSTER_NAME"
echo "  Then visit: http://localhost:8080"
echo ""
echo "To access OpenTelemetry Demo UI:"
echo "  kubectl port-forward -n otel-demo svc/my-otel-demo-frontendproxy 8081:8080 --context kind-$CLUSTER_NAME"
echo "  Then visit: http://localhost:8081"
echo ""
echo "To access Kubernetes Dashboard:"
echo "  kubectl proxy --context kind-$CLUSTER_NAME"
echo "  Then visit: http://localhost:8001/api/v1/namespaces/kubernetes-dashboard/services/https:kubernetes-dashboard:/proxy/"
echo ""
echo "To check metrics:"
echo "  kubectl top nodes --context kind-$CLUSTER_NAME"
echo "  kubectl top pods -n otel-demo --context kind-$CLUSTER_NAME"
