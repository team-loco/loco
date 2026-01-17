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

echo "Installing kubernetes-dashboard..."
helm repo add kubernetes-dashboard https://kubernetes.github.io/dashboard/
helm repo update
helm upgrade --install kubernetes-dashboard kubernetes-dashboard/kubernetes-dashboard \
  --create-namespace \
  --namespace kubernetes-dashboard \
  --kube-context "kind-$CLUSTER_NAME"

echo ""
echo "Cluster '$CLUSTER_NAME' is ready!"
echo ""
echo "To access the dashboard:"
echo "  kubectl proxy --context kind-$CLUSTER_NAME"
echo "  Then visit: http://localhost:8001/api/v1/namespaces/kubernetes-dashboard/services/https:kubernetes-dashboard:/proxy/"
echo ""
echo "To check node metrics:"
echo "  kubectl top nodes --context kind-$CLUSTER_NAME"
echo ""
echo "To check pod metrics:"
echo "  kubectl top pods --all-namespaces --context kind-$CLUSTER_NAME"
