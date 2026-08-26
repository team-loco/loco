#!/bin/bash
set -e

CLUSTER_NAME=${1:-test-cluster}

echo "Creating kind cluster: $CLUSTER_NAME"
kind create cluster --name "$CLUSTER_NAME" --config c.yaml

echo "Configuring CoreDNS to use 8.8.8.8..."
kubectl get configmap coredns -n kube-system -o yaml --context "kind-$CLUSTER_NAME" | \
  sed 's|forward . /etc/resolv.conf|forward . 8.8.8.8|g' | \
  kubectl apply -f - --context "kind-$CLUSTER_NAME"

kubectl rollout restart deployment coredns -n kube-system --context "kind-$CLUSTER_NAME"

echo "Installing metrics-server..."
helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/
helm repo update
helm upgrade --install metrics-server metrics-server/metrics-server \
  --set args="{--kubelet-insecure-tls}" \
  --kube-context "kind-$CLUSTER_NAME"

echo "Installing headlamp..."
helm repo add headlamp https://headlamp-k8s.github.io/headlamp/
helm repo update
helm upgrade --install headlamp headlamp/headlamp \
  --create-namespace \
  --namespace headlamp \
  --kube-context "kind-$CLUSTER_NAME"

echo ""
echo "Cluster '$CLUSTER_NAME' is ready!"
echo ""
echo "To access the dashboard:"
echo "  kubectl port-forward -n headlamp svc/headlamp 4466:4466 --context kind-$CLUSTER_NAME"
echo "  Then visit: http://localhost:4466"
echo ""
echo "To check node metrics:"
echo "  kubectl top nodes --context kind-$CLUSTER_NAME"
echo ""
echo "To check pod metrics:"
echo "  kubectl top pods --all-namespaces --context kind-$CLUSTER_NAME"
