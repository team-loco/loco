kind create cluster --name cluster1 --config cluster1.yaml
kind create cluster --name cluster2 --config cluster2.yaml


kubectl --context kind-cluster1 apply \
  -f https://raw.githubusercontent.com/kubernetes-sigs/mcs-api/refs/tags/v0.3.0/config/crd/multicluster.x-k8s.io_serviceexports.yaml \
  -f https://raw.githubusercontent.com/kubernetes-sigs/mcs-api/refs/tags/v0.3.0/config/crd/multicluster.x-k8s.io_serviceimports.yaml

kubectl --context kind-cluster2 apply \
  -f https://raw.githubusercontent.com/kubernetes-sigs/mcs-api/refs/tags/v0.3.0/config/crd/multicluster.x-k8s.io_serviceimports.yaml \
  -f https://raw.githubusercontent.com/kubernetes-sigs/mcs-api/refs/tags/v0.3.0/config/crd/multicluster.x-k8s.io_serviceexports.yaml

kubectl config use-context kind-cluster1
helm upgrade -i cilium cilium/cilium --version 1.18.5 \
  --namespace kube-system \
  --kube-context kind-cluster1 \
  --set cluster.name=cluster1 \
  --set cluster.id=1 \
  --set clustermesh.useAPIServer=true \
  --set clustermesh.apiserver.service.type=NodePort \
  --set clustermesh.mcsapi.enabled=true \
  --set clustermesh.enableMCSAPISupport=true \
  --set clustermesh.mcsapi.corednsAutoConfigure=true \
  --set operator.replicas=1 \
  --set clustermesh.enableEndpointSliceSynchronization=true \
  --set clustermesh.config.enabled=true \
  --set ciliumEndpointSlice.enabled=true \
  --set k8s.requireIPv4PodCIDR=true \
  --set kubeProxyReplacement=true \
  --set ipam.mode=kubernetes

# for some reason need to provide deprecated mcs flag as well? not sure why.
kubectl config use-context kind-cluster2
helm upgrade -i cilium cilium/cilium --version 1.18.5 \
  --namespace kube-system \
  --kube-context kind-cluster2 \
  --set cluster.name=cluster2 \
  --set cluster.id=2 \
  --set clustermesh.useAPIServer=true \
  --set clustermesh.apiserver.service.type=NodePort \
  --set clustermesh.mcsapi.enabled=true \
  --set clustermesh.enableMCSAPISupport=true \
  --set clustermesh.mcsapi.corednsAutoConfigure=true \
  --set operator.replicas=1 \
  --set clustermesh.enableEndpointSliceSynchronization=true \
  --set clustermesh.config.enabled=true \
  --set ciliumEndpointSlice.enabled=true \
  --set k8s.requireIPv4PodCIDR=true \
  --set kubeProxyReplacement=true \
  --set ipam.mode=kubernetes

k delete secrets -n kube-system cilium-ca
kubectl --context=kind-cluster1 get secret -n kube-system cilium-ca -o yaml | \
  kubectl --context kind-cluster2 create -f -

cilium clustermesh connect --context kind-cluster1 --destination-context kind-cluster2

kubectl config use-context kind-cluster1
helm install eg oci://docker.io/envoyproxy/gateway-helm --version v1.6.1 \
  -n envoy-gateway-system --create-namespace


kubectl config use-context kind-cluster2
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/gateway/latest/examples/kubernetes/application.yaml 
kubectl apply -f svc_export_cluster2.yaml

kubectl apply -f eg-mcs.yaml --context kind-cluster1
