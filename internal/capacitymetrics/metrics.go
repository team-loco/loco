// Package capacitymetrics defines the collector-to-query contract for effective pod reservations.
package capacitymetrics

const (
	CPU          = "loco_pod_cpu_request_cores"
	Memory       = "loco_pod_memory_request_bytes"
	Pods         = "loco_capacity_sample_pods"
	Path         = "/capacity"
	Interval     = "loco.collection.interval"
	Source       = "source_id"
	Node         = "node"
	PodUID       = "pod_uid"
	Pod          = "pod"
	Namespace    = "namespace"
	Organization = "organization_id"
	Workspace    = "workspace_id"
	Environment  = "environment_id"
	Resource     = "resource_id"
	Build        = "build_id"
)
