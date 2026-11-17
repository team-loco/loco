package v1alpha1

import "testing"

func TestProblemMessages(t *testing.T) {
	reasons := []ProblemReason{
		ProblemBuildFailed, ProblemApplyFailed, ProblemUnschedulable,
		ProblemImagePullFailed, ProblemImageUser, ProblemConfigError,
		ProblemCrashLoop, ProblemOutOfMemory, ProblemHealthCheckFailing,
		ProblemRolloutTimeout, ProblemUncleanShutdown,
	}
	counts := make(map[ProblemReason]int)
	for _, problem := range problems {
		counts[problem.Reason]++
	}
	if len(problems) != len(reasons) {
		t.Fatalf("problem rows = %d, want %d", len(problems), len(reasons))
	}
	for _, reason := range reasons {
		if counts[reason] != 1 {
			t.Errorf("reason %q has %d rows, want 1", reason, counts[reason])
		}
		if message, ok := ProblemMessage(reason); !ok || message == "" {
			t.Errorf("reason %q has no public message", reason)
		}
	}
	for _, reason := range []ProblemReason{"", "unknown"} {
		if _, ok := ProblemMessage(reason); ok {
			t.Errorf("accepted unknown reason %q", reason)
		}
	}
}

func TestProblemHigherPriority(t *testing.T) {
	for _, pair := range [][2]ProblemReason{
		{ProblemBuildFailed, ProblemApplyFailed},
		{ProblemOutOfMemory, ProblemCrashLoop},
		{ProblemRolloutTimeout, ProblemUncleanShutdown},
	} {
		if !ProblemHigherPriority(pair[0], pair[1]) {
			t.Errorf("%q should outrank %q", pair[0], pair[1])
		}
		if ProblemHigherPriority(pair[1], pair[0]) {
			t.Errorf("%q should not outrank %q", pair[1], pair[0])
		}
		if ProblemHigherPriority(pair[0], pair[0]) {
			t.Errorf("%q should not outrank itself", pair[0])
		}
	}
	if ProblemHigherPriority("unknown", ProblemCrashLoop) || ProblemHigherPriority(ProblemCrashLoop, "unknown") {
		t.Error("compared an unknown reason")
	}
}
