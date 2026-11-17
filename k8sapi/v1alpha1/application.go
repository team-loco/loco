package v1alpha1

const (
	workloadNamePrefix           = "resource-"
	FinalizerAppResourcesCleanup = "infra.loco.io/app-resources-cleanup"
)

// ApplicationName identifies one placement in the control namespace.
func ApplicationName(placementID string) string {
	return "placement-" + placementID
}

// WorkloadName identifies a resource's objects within its environment namespace.
func WorkloadName(resourceID string) string {
	return workloadNamePrefix + resourceID
}

// EnvironmentNamespace names the workload namespace of one environment.
func EnvironmentNamespace(prefix, environmentID string) string {
	return prefix + environmentID
}
