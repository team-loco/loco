package v1alpha1

// Runtime problem reasons are Loco's public vocabulary, independent of Kubernetes diagnostics.
const (
	RuntimeCrashLooping       = "crash_looping"
	RuntimeOutOfMemory        = "out_of_memory"
	RuntimeImagePullFailed    = "image_pull_failed"
	RuntimeUnschedulable      = "unschedulable"
	RuntimeHealthCheckFailing = "health_check_failing"
)

// RuntimeProblemMessage returns the public message for a runtime reason.
func RuntimeProblemMessage(reason string) (string, bool) {
	switch reason {
	case "":
		return "", true
	case RuntimeCrashLooping:
		return "The service keeps exiting soon after it starts. Check its logs for the error.", true
	case RuntimeOutOfMemory:
		return "The service ran out of memory. Increase its memory limit or reduce memory usage.", true
	case RuntimeImagePullFailed:
		return "Loco could not pull the image. Check the image name and registry access.", true
	case RuntimeUnschedulable:
		return "The service cannot be scheduled. Check the region's capacity and the service's resource requests.", true
	case RuntimeHealthCheckFailing:
		return "The service is failing its health check, or without one is not accepting connections on its port. " +
			"Check its port, health check settings and logs.", true
	default:
		return "", false
	}
}
