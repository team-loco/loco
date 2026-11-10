package v1alpha1

import "testing"

func TestRuntimeProblemMessages(t *testing.T) {
	reasons := []string{
		RuntimeCrashLooping,
		RuntimeOutOfMemory,
		RuntimeImagePullFailed,
		RuntimeUnschedulable,
		RuntimeHealthCheckFailing,
	}
	for _, reason := range reasons {
		message, ok := RuntimeProblemMessage(reason)
		if !ok || message == "" {
			t.Fatalf("reason %q has no public message", reason)
		}
	}
	if message, ok := RuntimeProblemMessage(""); !ok || message != "" {
		t.Fatalf("clear condition = %q, %v", message, ok)
	}
	if _, ok := RuntimeProblemMessage("CrashLoopBackOff"); ok {
		t.Fatal("accepted a Kubernetes reason")
	}
}
