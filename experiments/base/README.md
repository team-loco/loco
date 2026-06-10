# Base Testing Cluster

A minimal kind cluster setup with monitoring capabilities for quick experimentation.

## What's Included

- 1 control-plane node
- 2 worker nodes
- metrics-server for resource metrics
- headlamp for cluster inspection

## Quick Start

```bash
./setup.sh [cluster-name]
```

Default cluster name is `test-cluster` if not specified.

## Usage

### Create cluster
```bash
./setup.sh my-cluster
```

### Check cluster status
```bash
kubectl get nodes --context kind-my-cluster
kubectl top nodes --context kind-my-cluster
```

### Access dashboard
```bash
kubectl port-forward -n headlamp svc/headlamp 4466:4466 --context kind-my-cluster
```
Then visit: http://localhost:4466

### Delete cluster
```bash
kind delete cluster --name my-cluster
```

## Copying for New Experiments

This base setup is designed to be copied and extended:

```bash
cp -r experiments/base experiments/my-experiment
cd experiments/my-experiment
# Modify setup.sh to add your experiment-specific components
```
