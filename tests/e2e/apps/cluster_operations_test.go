package apps

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/internal/locofile"
	podresource "k8s.io/component-helpers/resource"

	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/tests/e2e/platform"
	corev1 "k8s.io/api/core/v1"
)

func TestClusterCapacityReachesOperators(t *testing.T) {
	t.Parallel()
	var nodes corev1.NodeList
	p.MustGet(t, &nodes, "nodes")
	var cpu, memory int64
	var pods int32
	for _, node := range nodes.Items {
		cpu += node.Status.Allocatable.Cpu().MilliValue()
		memory += node.Status.Allocatable.Memory().Value()
		pods += int32(node.Status.Allocatable.Pods().Value())
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if err := platform.Poll(ctx, func(ctx context.Context) error {
		response, err := p.Admin.ClusterAdmin.ListClusters(ctx, &clusterv1.ListClustersRequest{})
		if err != nil {
			return err
		}
		for _, cluster := range response.GetClusters() {
			if cluster.GetId() != p.ClusterID {
				continue
			}
			capacity := cluster.GetCapacity()
			if capacity == nil || capacity.GetStale() {
				return fmt.Errorf("capacity sample is not fresh: %v", capacity)
			}
			if capacity.GetCpuMillicoresAllocatable() != cpu || capacity.GetMemoryBytesAllocatable() != memory ||
				capacity.GetPodsAllocatable() != pods {
				return fmt.Errorf(
					"capacity %v differs from real nodes: CPU %d memory %d pods %d",
					capacity,
					cpu,
					memory,
					pods,
				)
			}
			if capacity.GetPodsAssigned() < capacity.GetPodsRunning() || capacity.GetCpuMillicoresRequested() <= 0 ||
				capacity.GetMemoryBytesRequested() <= 0 {
				return fmt.Errorf("capacity reservation sample incomplete: %v", capacity)
			}
			_, err = p.Admin.ClusterAdmin.GetClusterOperations(
				ctx,
				&clusterv1.GetClusterOperationsRequest{ClusterId: cluster.GetId()},
			)
			return err
		}
		return fmt.Errorf("cluster %s is not listed", p.ClusterID)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNodeAllocationsReachOperators(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	dir := t.TempDir()
	const resource = "node-allocation"
	platform.WriteLocoFile(
		t,
		dir,
		locofile.File{
			Partial:  resource,
			Services: map[string]locofile.Service{resource: platform.ImageService(p.PublicImage)},
		},
	)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	id := w.ResourceID(t, resource)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	err := platform.Poll(ctx, func(ctx context.Context) error {
		nodes, err := p.Admin.ClusterAdmin.ListClusterNodes(
			ctx,
			&clusterv1.ListClusterNodesRequest{ClusterId: p.ClusterID},
		)
		if err != nil {
			return err
		}
		if nodes.GetSample() == nil || nodes.GetSample().GetStale() {
			return fmt.Errorf("no fresh node capacity: %v", nodes)
		}
		for _, node := range nodes.GetNodes() {
			out, err := p.Admin.ClusterAdmin.GetNodeAllocations(
				ctx,
				&clusterv1.GetNodeAllocationsRequest{
					ClusterId:   p.ClusterID,
					Node:        node.GetName(),
					SampleId:    nodes.GetSample().GetId(),
					WorkspaceId: w.ID,
				},
			)
			if err != nil {
				return err
			}
			for _, consumer := range out.GetConsumers() {
				if consumer.GetId() != id {
					continue
				}
				if consumer.GetCpuMillicores() <= 0 || consumer.GetMemoryBytes() <= 0 ||
					consumer.GetResourceCount() != 1 {
					return fmt.Errorf("resource allocation incomplete: %v", consumer)
				}
				var pods corev1.PodList
				p.MustGet(t, &pods, "-n", w.Namespace, "pods", "-l", "loco.io/resource-id="+id)
				var cpu, memory int64
				for index := range pods.Items {
					pod := &pods.Items[index]
					if pod.Spec.NodeName != node.GetName() || pod.Status.Phase == corev1.PodSucceeded ||
						pod.Status.Phase == corev1.PodFailed {
						continue
					}
					requests := podresource.PodRequests(pod, podresource.PodResourcesOptions{})
					cpu += requests.Cpu().MilliValue()
					memory += requests.Memory().Value()
				}
				if consumer.GetCpuMillicores() != cpu || consumer.GetMemoryBytes() != memory {
					return fmt.Errorf(
						"reported %dm/%d bytes differs from actual node pod requests %dm/%d bytes",
						consumer.GetCpuMillicores(),
						consumer.GetMemoryBytes(),
						cpu,
						memory,
					)
				}
				allocations, err := p.User.Resource.ListResourceAllocations(
					ctx,
					&resourcev1.ListResourceAllocationsRequest{
						WorkspaceId:   w.ID,
						EnvironmentId: w.EnvironmentID,
						ResourceIds:   []string{id},
					},
				)
				if err != nil {
					return err
				}
				if len(allocations.GetResources()) != 1 {
					return fmt.Errorf("resource reservation missing: %v", allocations)
				}
				actual := allocations.GetResources()[0]
				if actual.GetCpuMillicores() != cpu || actual.GetMemoryBytes() != memory {
					return fmt.Errorf("environment reservation differs from pod requests: %v", actual)
				}
				t.Logf(
					"resource %s on %s: actual pod requests reached operator API: %dm CPU, %d bytes",
					id,
					node.GetName(),
					cpu,
					memory,
				)
				return nil
			}
		}
		return fmt.Errorf("resource %s not yet reported", id)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.User.ClusterAdmin.ListClusterNodes(
		t.Context(),
		&clusterv1.ListClusterNodesRequest{ClusterId: p.ClusterID},
	)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("tenant reads system capacity: %v", err)
	}
}
