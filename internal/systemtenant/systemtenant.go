// Package systemtenant names the tenant that holds Loco's own telemetry, apart from every workspace.
package systemtenant

const (
	// WorkspaceID is the loco.workspace.id ingest stamps on Loco's own telemetry; no workspace has it.
	WorkspaceID = "00000000-0000-0000-0000-000000000001"
	// ScopeAttribute marks a resource's telemetry as Loco's own when it holds PlatformScope.
	ScopeAttribute = "loco.telemetry.scope"
	PlatformScope  = "platform"
	// ControlPlaneClusterID is the loco.cluster.id of telemetry the control plane's own components send.
	ControlPlaneClusterID = "control-plane"
)
