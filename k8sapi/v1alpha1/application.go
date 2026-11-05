package v1alpha1

const (
	applicationNamePrefix = "resource-"

	ApplicationPhaseDeploying = "Deploying"
	ApplicationPhaseReady     = "Ready"
	ApplicationPhaseFailed    = "Failed"
)

// ApplicationName is the name of the Application, and of the workload objects derived from it, for a resource.
func ApplicationName(resourceID string) string {
	return applicationNamePrefix + resourceID
}
